package desktop

import (
	"cmp"
	"encoding/json/v2"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
	"uuid"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/yeekui"
	"yeek/internal/engine"
	"yeek/locales"
)

type networkEditState struct {
	source, pending, error string
	generation             uint64
}

func networkJSON(value any) string {
	data, _ := json.Marshal(value, json.Deterministic(true))
	return string(data)
}
func (s *networkEditState) refresh(value any) bool {
	current := networkJSON(value)
	if s.pending != "" {
		return false
	}
	if s.source == current {
		return false
	}
	s.source = current
	return true
}
func (a *App) saveNetworkField(state *networkEditState, model engine.Object, key string, value any) {
	snapshot := networkJSON(value)
	state.pending = snapshot
	state.error = ""
	state.generation++
	generation := state.generation
	patch := engine.Object{"model": s(model, "model"), "id": s(model, "id"), "createdAt": model["createdAt"], key: value}
	patch = deepCopy(patch)
	a.run(func() (func(), error) {
		saved, err := a.Engine.Save(a.ctx, patch)
		return func() {
			if generation != state.generation {
				return
			}
			if err != nil {
				state.error = err.Error()
				return
			}
			a.applyModel(saved)
			state.source = snapshot
			state.pending = ""
		}, nil
	})
}

type certificateDraft struct {
	id, host, port, kind, crt, key, pfx, password, error, info string
	enabled, open, busy                                        bool
}
type certificateEditor struct {
	sync networkEditState
	rows []*certificateDraft
}

func certificateModel(d *certificateDraft) (engine.Object, error) {
	var port any
	if d.port != "" {
		value, err := strconv.Atoi(d.port)
		if err != nil || value < 1 || value > 65535 {
			return nil, errors.New(locales.PortMustBeBetween1And())
		}
		port = value
	}
	return engine.Object{"host": strings.TrimSpace(d.host), "port": port, "crtFile": nilIfEmpty(d.crt), "keyFile": nilIfEmpty(d.key), "pfxFile": nilIfEmpty(d.pfx), "passphrase": nilIfEmpty(d.password), "enabled": d.enabled}, nil
}
func (a *App) certificateSettings(c *ui.Context, p colors) {
	if a.certificates == nil {
		a.certificates = &certificateEditor{}
	}
	state := a.certificates
	if state.sync.refresh(a.settings["clientCertificates"]) {
		state.rows = nil
		for _, model := range oslice(a.settings, "clientCertificates") {
			d := &certificateDraft{id: uuid.NewV4().String(), host: s(model, "host"), crt: s(model, "crtFile"), key: s(model, "keyFile"), pfx: s(model, "pfxFile"), password: s(model, "passphrase"), enabled: model["enabled"] != false, kind: "PEM"}
			if model["port"] != nil {
				d.port = strconv.FormatFloat(n(model, "port"), 'f', -1, 64)
			}
			if d.pfx != "" {
				d.kind = "PFX / PKCS#12"
			}
			d.open = d.host == ""
			state.rows = append(state.rows, d)
		}
	}
	changed := false
	ui.Row(c).Gap(12).Children(func() {
		ui.Text(c, locales.ClientCertificates()).FontSize(18).FontWeight(600).Grow(1)
		if ui.Button(c, locales.AddCertificate()).Clicked() {
			state.rows = append(state.rows, &certificateDraft{id: uuid.NewV4().String(), kind: "PEM", enabled: true, open: true})
			changed = true
		}
	})
	remove := -1
	for i, d := range state.rows {
		ui.Column(c).Key(d.id).Padding(12).Gap(10).Border(1, p.border).Radius(5).Children(func() {
			toggleOpen := false
			defer func() {
				if toggleOpen {
					d.open = !d.open
				}
			}()
			ui.Row(c).Gap(8).Children(func() {
				if ui.Checkbox(c, &d.enabled, "").Label(locales.EnableCertificateIndex(i + 1)).Changed() {
					changed = true
				}
				label := cmp.Or(d.host, locales.ConfigureCertificate())
				if d.port != "" {
					label += ":" + d.port
				}
				if ui.ButtonBase(c).Grow(1).Children(func() { ui.Text(c, label).Font("monospace").FontSize(12) }).Clicked() {
					toggleOpen = true
				}
				if smallIconButton(c, "close", locales.RemoveCertificateIndex(i+1)).Clicked() {
					remove = i
				}
			})
			if !d.open {
				return
			}
			ui.Row(c).Gap(10).Children(func() {
				ui.Column(c).Grow(1).Gap(4).Children(func() {
					ui.Text(c, locales.Host()).FontSize(11).TextColor(p.muted)
					if ui.TextInput(c, &d.host).Label(locales.CertificateHostIndex(i + 1)).Placeholder("api.example.com").FillWidth().Changed() {
						changed = true
						d.info = ""
					}
				})
				ui.Column(c).Width(115).Gap(4).Children(func() {
					ui.Text(c, locales.Port()).FontSize(11).TextColor(p.muted)
					if ui.TextInput(c, &d.port).Label(locales.CertificatePortIndex(i + 1)).Placeholder(locales.AllPorts()).FillWidth().Changed() {
						changed = true
						d.info = ""
					}
				})
			})
			if ui.Select(c, &d.kind, []string{"PEM", "PFX / PKCS#12"}).Label(locales.CertificateFormatIndex(i + 1)).Changed() {
				if d.kind == "PEM" {
					d.pfx = ""
				} else {
					d.crt, d.key = "", ""
				}
				changed = true
				d.info = ""
			}
			fileChanged := func() { d.info = ""; a.persistCertificates() }
			if d.kind == "PEM" {
				if a.networkFile(c, p, &d.crt, locales.CertificateFileIndex(i+1), locales.Certificate(), []string{"pem", "crt", "cer"}, fileChanged) {
					changed = true
					d.info = ""
				}
				if a.networkFile(c, p, &d.key, locales.PrivateKeyFileIndex(i+1), locales.PrivateKey(), []string{"pem", "key"}, fileChanged) {
					changed = true
					d.info = ""
				}
			} else {
				if a.networkFile(c, p, &d.pfx, locales.PFXFileIndex(i+1), "PFX / PKCS#12", []string{"pfx", "p12"}, fileChanged) {
					changed = true
					d.info = ""
				}
			}
			ui.Text(c, locales.Passphrase()).FontSize(11).TextColor(p.muted)
			if ui.TextInput(c, &d.password).Password().Label(locales.CertificatePassphraseIndex(i + 1)).FillWidth().Changed() {
				changed = true
				d.info = ""
			}
			model, err := certificateModel(d)
			if err == nil && d.host != "" && (d.crt != "" || d.key != "" || d.pfx != "") {
				err = engine.ValidateClientCertificate(model)
			}
			if err != nil {
				ui.Text(c, err.Error()).FontSize(11).TextColor(p.red)
			}
			if d.error != "" {
				ui.Text(c, d.error).FontSize(11).TextColor(p.red).MaxLines(3)
			}
			if d.info != "" {
				ui.Text(c, d.info).FontSize(11).TextColor(p.muted).Selectable()
			}
			if ui.Button(c, locales.CheckCertificate()).Label(locales.CheckCertificateIndex(i + 1)).Disabled(d.busy || err != nil || d.host == "").FontSize(11).Clicked() {
				a.checkCertificate(d)
			}
		})
	}
	if remove >= 0 {
		state.rows = slices.Delete(state.rows, remove, remove+1)
		changed = true
	}
	if changed {
		a.persistCertificates()
	}
	if state.sync.error != "" {
		ui.Text(c, state.sync.error).TextColor(p.red).FontSize(12)
	}
}
func (a *App) persistCertificates() {
	if a.deferUntilInputs(a.persistCertificates) {
		return
	}
	state := a.certificates
	if state == nil {
		return
	}
	values := []any{}
	for _, d := range state.rows {
		m, err := certificateModel(d)
		if err != nil {
			state.sync.error = err.Error()
			return
		}
		values = append(values, m)
	}
	a.saveNetworkField(&state.sync, a.settings, "clientCertificates", values)
}
func (a *App) checkCertificate(d *certificateDraft) {
	if a.deferUntilInputs(func() { a.checkCertificate(d) }) {
		return
	}
	model, err := certificateModel(d)
	if err != nil {
		d.error = err.Error()
		return
	}
	snapshot := networkJSON(model)
	d.busy = true
	d.error = ""
	a.background(func() (func(), error) {
		pair, err := engine.LoadClientCertificate(model)
		return func() {
			d.busy = false
			current, _ := certificateModel(d)
			if networkJSON(current) != snapshot {
				return
			}
			if err != nil {
				d.error = err.Error()
				return
			}
			if pair.Leaf != nil {
				d.info = locales.SubjectSubjectIssuerIssuerExpiresExpires(pair.Leaf.Subject.String(), pair.Leaf.Issuer.String(), pair.Leaf.NotAfter.Local().Format(time.RFC3339))
			}
		}, nil
	})
}

func (a *App) networkFile(c *ui.Context, p colors, value *string, label, title string, extensions []string, selected func()) bool {
	changed := false
	ui.Column(c).Gap(4).Children(func() {
		ui.Text(c, title).FontSize(11).TextColor(p.muted)
		ui.Row(c).Gap(7).Children(func() {
			changed = ui.TextInput(c, value).Label(label).Grow(1).MinWidth(0).FontSize(12).Changed()
			if ui.Button(c, locales.BrowseEllipsis()).Label(locales.BrowseField(label)).FontSize(11).Clicked() {
				a.background(func() (func(), error) {
					paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: a.Window, Title: title, Filters: []mygo.FileFilter{{Name: title, Extensions: extensions}}})
					return func() {
						if len(paths) > 0 {
							*value = paths[0]
							selected()
						}
					}, err
				})
			}
			if *value != "" && smallIconButton(c, "close", locales.ClearField(label)).Clicked() {
				*value = ""
				changed = true
			}
		})
	})
	return changed
}

type proxyEditor struct {
	sync                                      networkEditState
	mode, http, https, bypass, user, password string
	enabled, auth                             bool
}

func (a *App) proxySettingsView(c *ui.Context, p colors) {
	if a.proxyEditor == nil {
		a.proxyEditor = &proxyEditor{}
	}
	d := a.proxyEditor
	if d.sync.refresh(a.settings["proxy"]) {
		proxy := o(a.settings, "proxy")
		d.mode = "automatic"
		if s(proxy, "type") == "disabled" {
			d.mode = "disabled"
		}
		if s(proxy, "type") == "enabled" {
			d.mode = "enabled"
		}
		d.http, d.https, d.bypass = s(proxy, "http"), s(proxy, "https"), s(proxy, "bypass")
		d.enabled = !b(proxy, "disabled")
		d.auth = proxy["auth"] != nil
		d.user, d.password = s(o(proxy, "auth"), "user"), s(o(proxy, "auth"), "password")
	}
	ui.Text(c, locales.Proxy()).FontSize(18).FontWeight(600)
	modes, labels := []string{"automatic", "disabled", "enabled"}, []string{locales.Automatic(), locales.NoProxy(), locales.Custom()}
	label := labels[max(0, slices.Index(modes, d.mode))]
	changed := ui.Select(c, &label, labels).Label(locales.ProxyMode()).Changed()
	nextMode, nextAuth := modes[max(0, slices.Index(labels, label))], d.auth
	if d.mode == "automatic" {
		ui.Text(c, locales.UsesHTTPPROXYHTTPSPROXYAnd()).FontSize(12).TextColor(p.muted)
	}
	if d.mode == "enabled" {
		if ui.Checkbox(c, &d.enabled, locales.EnableProxy()).Changed() {
			changed = true
		}
		for _, field := range []struct {
			label       string
			value       *string
			placeholder string
		}{{locales.HTTPProxy(), &d.http, "localhost:9090"}, {locales.HTTPSProxy(), &d.https, "localhost:9090"}, {locales.ProxyBypass(), &d.bypass, "localhost, *.example.com, 10.0.0.0/8"}} {
			ui.Column(c).Gap(4).Children(func() {
				ui.Text(c, field.label).FontSize(12).TextColor(p.muted)
				if ui.TextInput(c, field.value).Label(locales.FieldValue(field.label)).Placeholder(field.placeholder).FillWidth().Changed() {
					changed = true
				}
			})
		}
		if ui.Checkbox(c, &nextAuth, locales.ProxyAuthentication()).Changed() {
			changed = true
		}
		if d.auth {
			ui.Text(c, locales.Username()).FontSize(12).TextColor(p.muted)
			if ui.TextInput(c, &d.user).Label(locales.ProxyUsername()).FillWidth().Changed() {
				changed = true
			}
			ui.Text(c, locales.Password()).FontSize(12).TextColor(p.muted)
			if ui.TextInput(c, &d.password).Password().Label(locales.ProxyPassword()).FillWidth().Changed() {
				changed = true
			}
		}
		for _, value := range []string{d.http, d.https} {
			if _, err := engine.NormalizeProxyURL(value); err != nil {
				ui.Text(c, err.Error()).FontSize(11).TextColor(p.red)
			}
		}
	}
	if changed {
		d.mode, d.auth = nextMode, nextAuth
		a.persistProxy()
	}
	if d.sync.error != "" {
		ui.Text(c, d.sync.error).FontSize(12).TextColor(p.red)
	}
}
func (a *App) persistProxy() {
	if a.deferUntilInputs(a.persistProxy) {
		return
	}
	d := a.proxyEditor
	var value any
	if d.mode == "disabled" {
		value = engine.Object{"type": "disabled"}
	}
	if d.mode == "enabled" {
		var auth any
		if d.auth {
			auth = engine.Object{"user": d.user, "password": d.password}
		}
		value = engine.Object{"type": "enabled", "disabled": !d.enabled, "http": d.http, "https": d.https, "bypass": d.bypass, "auth": auth}
	}
	a.saveNetworkField(&d.sync, a.settings, "proxy", value)
}

type dnsDraft struct {
	id, host, ipv4, ipv6 string
	enabled              bool
}
type workspaceNetworkEditor struct {
	dnsSync, caSync networkEditState
	rows            []*dnsDraft
	ca, error       string
	checking        bool
}

func (a *App) workspaceNetwork(id string) *workspaceNetworkEditor {
	if a.workspaceNetworkEditors == nil {
		a.workspaceNetworkEditors = map[string]*workspaceNetworkEditor{}
	}
	if a.workspaceNetworkEditors[id] == nil {
		a.workspaceNetworkEditors[id] = &workspaceNetworkEditor{}
	}
	return a.workspaceNetworkEditors[id]
}
func commaAddresses(value string) []any {
	result := []any{}
	for part := range strings.SplitSeq(value, ",") {
		if part = strings.TrimSpace(part); part != "" {
			result = append(result, part)
		}
	}
	return result
}
func addressText(value []any) string {
	parts := []string{}
	for _, v := range value {
		parts = append(parts, fmt.Sprint(v))
	}
	return strings.Join(parts, ", ")
}
func (a *App) dnsSettings(c *ui.Context, p colors, workspace engine.Object) {
	id := s(workspace, "id")
	d := a.workspaceNetwork(id)
	if d.dnsSync.refresh(workspace["settingDnsOverrides"]) {
		d.rows = nil
		for _, entry := range oslice(workspace, "settingDnsOverrides") {
			d.rows = append(d.rows, &dnsDraft{id: uuid.NewV4().String(), host: s(entry, "hostname"), ipv4: addressText(networkArray(entry, "ipv4")), ipv6: addressText(networkArray(entry, "ipv6")), enabled: entry["enabled"] != false})
		}
	}
	ui.Text(c, locales.DNSOverrides()).FontSize(18).FontWeight(600)
	changed := false
	remove := -1
	for i, row := range d.rows {
		ui.Column(c).Key(row.id).Gap(7).Padding(10).Border(1, p.border).Radius(4).Children(func() {
			ui.Row(c).Gap(7).Children(func() {
				if ui.Checkbox(c, &row.enabled, "").Label(locales.EnableDNSOverrideIndex(i + 1)).Changed() {
					changed = true
				}
				if ui.TextInput(c, &row.host).Label(locales.DNSHostnameIndex(i + 1)).Placeholder("api.example.com").Grow(1).Changed() {
					changed = true
				}
				if smallIconButton(c, "close", locales.RemoveDNSOverrideIndex(i+1)).Clicked() {
					remove = i
				}
			})
			for _, field := range []struct {
				label       string
				value       *string
				placeholder string
			}{{locales.IPv4Addresses(), &row.ipv4, "127.0.0.1"}, {locales.IPv6Addresses(), &row.ipv6, "::1"}} {
				ui.Text(c, field.label).TextColor(p.muted).FontSize(11)
				if ui.TextInput(c, field.value).Label(locales.FieldIndex(field.label, i+1)).Placeholder(field.placeholder).FillWidth().Changed() {
					changed = true
				}
			}
			model := engine.Object{"hostname": row.host, "ipv4": commaAddresses(row.ipv4), "ipv6": commaAddresses(row.ipv6)}
			if row.enabled && (row.host != "" || row.ipv4 != "" || row.ipv6 != "") {
				if err := engine.ValidateDNSOverride(model); err != nil {
					ui.Text(c, err.Error()).TextColor(p.red).FontSize(11)
				}
			}
		})
	}
	if remove >= 0 {
		d.rows = slices.Delete(d.rows, remove, remove+1)
		changed = true
	}
	if ui.Button(c, locales.AddDNSOverride()).Clicked() {
		d.rows = append(d.rows, &dnsDraft{id: uuid.NewV4().String(), enabled: true})
		changed = true
	}
	if changed {
		a.persistDNS(id)
	}
	if d.dnsSync.error != "" {
		ui.Text(c, d.dnsSync.error).TextColor(p.red).FontSize(12)
	}
}
func (a *App) persistDNS(id string) {
	if a.deferUntilInputs(func() { a.persistDNS(id) }) {
		return
	}
	d := a.workspaceNetwork(id)
	values := []any{}
	for _, row := range d.rows {
		values = append(values, engine.Object{"hostname": row.host, "ipv4": commaAddresses(row.ipv4), "ipv6": commaAddresses(row.ipv6), "enabled": row.enabled})
	}
	a.saveNetworkField(&d.dnsSync, a.models[id], "settingDnsOverrides", values)
}
func (a *App) caSettings(c *ui.Context, p colors, workspace engine.Object) {
	id := s(workspace, "id")
	d := a.workspaceNetwork(id)
	if d.caSync.refresh(workspace["caFile"]) {
		d.ca = s(workspace, "caFile")
	}
	ui.Text(c, locales.AdditionalCACertificates()).FontSize(18).FontWeight(600)
	ui.Text(c, locales.TrustAPEMBundleOrDER()).FontSize(12).TextColor(p.muted)
	if a.networkFile(c, p, &d.ca, locales.CAFilePath(), locales.CAFile(), []string{"pem", "crt", "cer"}, func() { a.persistCA(id) }) {
		a.persistCA(id)
		d.error = ""
	}
	if ui.Button(c, locales.CheckCAFile()).Disabled(d.ca == "" || d.checking).AlignSelf(ui.Start).Clicked() {
		path := d.ca
		d.checking = true
		a.background(func() (func(), error) {
			summary, err := engine.CAFileSummary(path)
			return func() {
				d.checking = false
				if d.ca == path {
					d.error = ""
					if err != nil {
						d.error = err.Error()
					} else {
						d.error = summary
					}
				}
			}, nil
		})
	}
	if d.error != "" {
		ui.Text(c, d.error).FontSize(12).Selectable()
	}
	if d.caSync.error != "" {
		ui.Text(c, d.caSync.error).FontSize(12).TextColor(p.red)
	}
}
func (a *App) persistCA(id string) {
	if a.deferUntilInputs(func() { a.persistCA(id) }) {
		return
	}
	d := a.workspaceNetwork(id)
	a.saveNetworkField(&d.caSync, a.models[id], "caFile", nilIfEmpty(d.ca))
}

func networkArray(m engine.Object, key string) []any { v, _ := m[key].([]any); return v }

func (a *App) saveSetting(key string, value any) {
	a.settings[key] = value
	a.saveModel(engine.Object{"model": "settings", "id": "default", key: value})
}
