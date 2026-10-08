package desktop

import (
	"context"
	"errors"
	"net/url"

	"github.com/egoist/mygo"
	"yeek/internal/engine"
)

func (a *App) oauthBrowser(ctx context.Context, authorization engine.OAuthAuthorization) (string, error) {
	type result struct {
		url string
		err error
	}
	completed := make(chan result, 1)
	if a.Window == nil || a.Window.IsDestroyed() {
		return "", errors.New("open a Yeek window to authorize")
	}
	var root *mygo.Window
	counted := false
	var stopWatching func()
	windows := map[*mygo.Window]bool{}
	a.Window.Update(func() {
		if ctx.Err() != nil {
			return
		}
		settled := false
		finish := func(raw string, err error) {
			if settled {
				return
			}
			settled = true
			completed <- result{raw, err}
			if root != nil && !root.IsDestroyed() {
				root.Close()
			}
		}
		attach := func(window *mygo.Window) {
			windows[window] = true
			page := window.Page()
			if page == nil {
				return
			}
			page.OnWillNavigate(func(event *mygo.NavigateEvent) {
				if engine.OAuthCallbackMatches(event.URL, authorization.RedirectURI) {
					event.PreventDefault()
					finish(event.URL, nil)
					return
				}
				u, err := url.Parse(event.URL)
				if err != nil || u.Scheme != "http" && u.Scheme != "https" {
					event.PreventDefault()
				}
			})
			page.OnDidNavigate(func(raw string) {
				if engine.OAuthCallbackMatches(raw, authorization.RedirectURI) {
					finish(raw, nil)
				}
			})
			page.SetWindowOpenHandler(func(request mygo.WindowOpenRequest) *mygo.WindowOptions {
				if engine.OAuthCallbackMatches(request.URL, authorization.RedirectURI) {
					finish(request.URL, nil)
					return nil
				}
				u, err := url.Parse(request.URL)
				if err != nil || u.Scheme != "http" && u.Scheme != "https" && request.URL != "about:blank" {
					return nil
				}
				return &mygo.WindowOptions{Title: "OAuth Sign-in", Width: 760, Height: 760, Parent: window}
			})
			page.OnDidFailLoad(func(failure *mygo.LoadError) {
				if engine.OAuthCallbackMatches(failure.URL, authorization.RedirectURI) {
					finish(failure.URL, nil)
				}
			})
		}
		stopWatching = mygo.App.OnWindowCreated(func(window *mygo.Window) {
			if windows[window.Parent()] {
				attach(window)
			}
		})
		root = mygo.NewWindow(mygo.WindowOptions{Title: "OAuth Sign-in", Width: 860, Height: 780, Parent: a.Window})
		a.oauthBrowserCount++
		counted = true
		attach(root)
		root.OnClosed(func() { finish("", errors.New("authorization window closed")) })
		if err := root.Page().LoadURL(authorization.URL); err != nil {
			finish("", err)
		}
	})
	defer func() {
		a.Window.Update(func() {
			if stopWatching != nil {
				stopWatching()
			}
			if root != nil && !root.IsDestroyed() {
				root.Close()
			}
			if counted {
				a.oauthBrowserCount--
				counted = false
			}
		})
	}()
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case value := <-completed:
		return value.url, value.err
	}
}
