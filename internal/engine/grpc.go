package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/bufbuild/protocompile"
	"github.com/jhump/protoreflect/v2/grpcreflect"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
)

type GRPCMethod struct {
	Name                             string
	ClientStreaming, ServerStreaming bool
	Example                          string
}
type GRPCService struct {
	Name    string
	Methods []GRPCMethod
}
type grpcSession struct {
	stream     grpc.ClientStream
	method     protoreflect.MethodDescriptor
	model      Object
	mu         sync.Mutex
	closedSend bool
}

func (e *Engine) grpcClient(ctx context.Context, id, environment string) (*grpc.ClientConn, resolvedRequest, context.Context, error) {
	request, err := e.Store.Get(ctx, id)
	if err != nil {
		return nil, resolvedRequest{}, ctx, err
	}
	r, err := e.resolve(ctx, request, environment)
	if err != nil {
		return nil, r, ctx, err
	}
	u, err := buildURL(r.Model)
	if err != nil {
		return nil, r, ctx, err
	}
	transport, err := e.transport(ctx, r)
	if err != nil {
		return nil, r, ctx, err
	}
	creds := insecure.NewCredentials()
	if u.Scheme == "https" || u.Scheme == "grpcs" {
		config, err := transport.tlsFor(u)
		if err != nil {
			return nil, r, ctx, err
		}
		config.ServerName = ""
		creds = credentials.NewTLS(config)
	}
	size := int(number(r.Settings, "settingRequestMessageSize"))
	if size <= 0 {
		size = 64 * 1024 * 1024
	}
	conn, err := grpc.NewClient("passthrough:///"+net.JoinHostPort(u.Hostname(), networkPort(u)), grpc.WithAuthority(u.Host), grpc.WithContextDialer(transport.grpcDialer(u)), grpc.WithTransportCredentials(creds), grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(size), grpc.MaxCallSendMsgSize(size)))
	if err != nil {
		return nil, r, ctx, err
	}
	h := http.Header{}
	for _, value := range objects(array(r.Model, "headers")) {
		if enabled(value) && str(value, "name") != "" {
			h.Add(str(value, "name"), str(value, "value"))
		}
	}
	req := (&http.Request{URL: u, Header: h}).WithContext(ctx)
	if err = e.authenticate(req, r.Model, r.oauthOptions(environment)); err != nil {
		_ = conn.Close()
		return nil, r, ctx, err
	}
	md := metadata.MD{}
	for k, vs := range h {
		md[strings.ToLower(k)] = vs
	}
	return conn, r, metadata.NewOutgoingContext(ctx, md), nil
}
func grpcDescriptors(ctx context.Context, conn *grpc.ClientConn, protoFiles []string) ([]protoreflect.ServiceDescriptor, error) {
	services := []protoreflect.ServiceDescriptor{}
	if len(protoFiles) > 0 {
		dirs, names := []string{}, []string{}
		for _, file := range protoFiles {
			dirs = append(dirs, filepath.Dir(file))
			names = append(names, filepath.Base(file))
		}
		compiler := protocompile.Compiler{Resolver: protocompile.WithStandardImports(&protocompile.SourceResolver{ImportPaths: dirs})}
		files, err := compiler.Compile(ctx, names...)
		if err != nil {
			return nil, err
		}
		for _, file := range files {
			for i := 0; i < file.Services().Len(); i++ {
				services = append(services, file.Services().Get(i))
			}
		}
		return services, nil
	}
	reflector := grpcreflect.NewClientAuto(ctx, conn)
	defer reflector.Reset()
	names, err := reflector.ListServices()
	if err != nil {
		return nil, fmt.Errorf("server reflection failed; select .proto files if reflection is disabled: %w", err)
	}
	for _, name := range names {
		file, err := reflector.FileContainingSymbol(name)
		if err != nil {
			return nil, err
		}
		for i := 0; i < file.Services().Len(); i++ {
			service := file.Services().Get(i)
			if service.FullName() == name {
				services = append(services, service)
			}
		}
	}
	return services, nil
}
func (e *Engine) ReflectGRPC(ctx context.Context, id, environment string, files []string) ([]GRPCService, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	conn, _, ctx, err := e.grpcClient(ctx, id, environment)
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()
	services, err := grpcDescriptors(ctx, conn, files)
	if err != nil {
		return nil, err
	}
	result := []GRPCService{}
	for _, service := range services {
		s := GRPCService{Name: string(service.FullName()), Methods: []GRPCMethod{}}
		for i := 0; i < service.Methods().Len(); i++ {
			method := service.Methods().Get(i)
			example, err := protojson.MarshalOptions{EmitUnpopulated: true, Indent: "  "}.Marshal(dynamicpb.NewMessage(method.Input()))
			if err != nil {
				return nil, err
			}
			s.Methods = append(s.Methods, GRPCMethod{Name: string(method.Name()), ClientStreaming: method.IsStreamingClient(), ServerStreaming: method.IsStreamingServer(), Example: string(example)})
		}
		result = append(result, s)
	}
	return result, nil
}
func (e *Engine) StartGRPC(ctx context.Context, id, environment string, files []string) (Object, error) {
	ctx, finish, err := e.begin(ctx, id)
	if err != nil {
		return nil, err
	}
	success := false
	defer func() {
		if !success {
			finish()
		}
	}()
	conn, r, ctx, err := e.grpcClient(ctx, id, environment)
	if err != nil {
		return nil, err
	}
	defer func() {
		if !success {
			_ = conn.Close()
		}
	}()
	reflectCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	services, err := grpcDescriptors(reflectCtx, conn, files)
	if err != nil {
		return nil, err
	}
	var method protoreflect.MethodDescriptor
	for _, service := range services {
		if string(service.FullName()) != str(r.Model, "service") {
			continue
		}
		method = service.Methods().ByName(protoreflect.Name(str(r.Model, "method")))
	}
	if method == nil {
		return nil, errors.New("select a gRPC service and method")
	}
	model, err := e.Save(ctx, Object{"model": "grpc_connection", "workspaceId": r.Model["workspaceId"], "requestId": id, "url": r.Model["url"], "service": r.Model["service"], "method": r.Model["method"], "state": "connected"})
	if err != nil {
		return nil, err
	}
	stream, err := conn.NewStream(ctx, &grpc.StreamDesc{StreamName: string(method.Name()), ClientStreams: method.IsStreamingClient(), ServerStreams: method.IsStreamingServer()}, "/"+str(r.Model, "service")+"/"+str(r.Model, "method"))
	if err != nil {
		model["state"] = "closed"
		model["error"] = err.Error()
		_, _ = e.Save(context.WithoutCancel(e.ctx), model)
		return nil, err
	}
	session := &grpcSession{stream: stream, method: method, model: model}
	e.mu.Lock()
	if e.grpcSessions == nil {
		e.grpcSessions = map[string]*grpcSession{}
	}
	e.grpcSessions[str(model, "id")] = session
	e.mu.Unlock()
	defer func() {
		if !success {
			e.mu.Lock()
			delete(e.grpcSessions, str(model, "id"))
			e.mu.Unlock()
			closed := clone(model)
			closed["state"] = "closed"
			if err != nil {
				closed["error"] = err.Error()
			}
			_, _ = e.Save(context.WithoutCancel(e.ctx), closed)
		}
	}()
	e.grpcEvent(ctx, model, "connection_start", "Connected", nil)
	if err = e.writeGRPC(ctx, session, str(r.Model, "message")); err != nil {
		return nil, err
	}
	if !method.IsStreamingClient() {
		if err = stream.CloseSend(); err != nil {
			return nil, err
		}
		session.closedSend = true
	}
	success = true
	started := time.Now()
	go func() {
		defer finish()
		defer func() { _ = conn.Close() }()
		headers, _ := stream.Header()
		if len(headers) > 0 {
			e.grpcEvent(ctx, model, "info", "Response headers", mdObject(headers))
		}
		var recvErr error
		for {
			message := dynamicpb.NewMessage(method.Output())
			if err := stream.RecvMsg(message); err != nil {
				recvErr = err
				break
			}
			data, err := protojson.MarshalOptions{Indent: "  "}.Marshal(message)
			if err != nil {
				recvErr = err
				break
			}
			e.grpcEvent(ctx, model, "server_message", string(data), nil)
		}
		closed := clone(model)
		closed["state"] = "closed"
		closed["elapsed"] = float64(time.Since(started).Milliseconds())
		closed["trailers"] = mdObject(stream.Trailer())
		code := status.Code(recvErr)
		if errors.Is(recvErr, io.EOF) {
			code = 0
		}
		closed["status"] = int(code)
		if code != 0 {
			closed["error"] = recvErr.Error()
			e.grpcEvent(e.ctx, model, "error", recvErr.Error(), nil)
		}
		e.grpcEvent(e.ctx, model, "connection_end", code.String(), mdObject(stream.Trailer()))
		_, _ = e.Save(context.WithoutCancel(e.ctx), closed)
		e.mu.Lock()
		delete(e.grpcSessions, str(model, "id"))
		e.mu.Unlock()
	}()
	return model, nil
}
func mdObject(md metadata.MD) Object {
	m := Object{}
	for k, v := range md {
		m[k] = strings.Join(v, ", ")
	}
	return m
}
func (e *Engine) grpcEvent(ctx context.Context, model Object, kind, text string, md Object) {
	if md == nil {
		md = Object{}
	}
	_, _ = e.Save(ctx, Object{"model": "grpc_event", "workspaceId": model["workspaceId"], "requestId": model["requestId"], "connectionId": model["id"], "eventType": kind, "content": text, "metadata": md})
}
func (e *Engine) writeGRPC(ctx context.Context, session *grpcSession, text string) error {
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.closedSend {
		return errors.New("request stream is already finished")
	}
	if strings.TrimSpace(text) == "" {
		text = "{}"
	}
	message := dynamicpb.NewMessage(session.method.Input())
	if err := protojson.Unmarshal([]byte(text), message); err != nil {
		return fmt.Errorf("request message: %w", err)
	}
	if err := session.stream.SendMsg(message); err != nil {
		return err
	}
	e.grpcEvent(ctx, session.model, "client_message", text, nil)
	return nil
}
func (e *Engine) SendGRPCMessage(ctx context.Context, id, text string) error {
	e.mu.Lock()
	session := e.grpcSessions[id]
	e.mu.Unlock()
	if session == nil {
		return errors.New("gRPC connection is closed")
	}
	if !session.method.IsStreamingClient() {
		return errors.New("this method accepts only one request message")
	}
	return e.writeGRPC(ctx, session, text)
}
func (e *Engine) FinishGRPC(id string) error {
	e.mu.Lock()
	session := e.grpcSessions[id]
	e.mu.Unlock()
	if session == nil {
		return errors.New("gRPC connection is closed")
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	session.closedSend = true
	return session.stream.CloseSend()
}
