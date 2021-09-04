package easyserver

import (
	"fmt"
	"net/http"
	"runtime/debug"
	"sort"
	"strings"

	"github.com/58kg/logs"
	"github.com/58kg/router"
	"github.com/58kg/to_string"
)

type Engine interface {
	Register(method, path string, handle router.Handler)
	AppendMiddleware(handler func(c Context))
	RunByHttps(port int, certFile, keyFile string) error
}

type Context interface {
	GetReq() *http.Request
	GetResp() http.ResponseWriter
	GetParamParam() []router.UrlParam
	Next() bool
}

func New() Engine {
	return &engine{
		r: router.New(),
	}
}

type engine struct {
	r              router.Router
	middlewares    *middleware
	allowedMethods struct {
		s   []string
		str string
	}
}

func (e *engine) Register(method, path string, handle router.Handler) {
	e.r.Register(method, path, handle)
	for _, v := range e.allowedMethods.s {
		if v == method {
			return
		}
	}
	e.allowedMethods.s = append(e.allowedMethods.s, method)
	sort.Strings(e.allowedMethods.s)
	e.allowedMethods.str = strings.Join(e.allowedMethods.s, ",")
}

func (e *engine) RunByHttps(port int, certFile, keyFile string) error {
	return http.ListenAndServeTLS(":"+fmt.Sprintf("%d", port), certFile, keyFile, e)
}

func (e *engine) ServeHTTP(resp http.ResponseWriter, req *http.Request) {
	logId := logs.GenLogId()
	req = req.WithContext(logs.CtxWithLogId(req.Context(), logId))
	defer func() {
		resp.Header().Set(logs.LogIdContextKey, logId)
		logs.CtxTrace(req.Context(), "Resp, [Header]=%v", to_string.String(resp.Header()))
	}()

	logs.CtxTrace(req.Context(), "Req, [Method]=%v, [URL]=%v, [Header]=%v, [Host]=%v, [Form]=%v, [PostForm]=%v, [MultipartForm]=%v, [Trailer]=%v, [RemoteAddr]=%v, [RequestURI]=%v",
		req.Method, to_string.String(req.URL), to_string.String(req.Header), req.Host, to_string.String(req.Form), to_string.String(req.PostForm),
		to_string.String(req.MultipartForm), to_string.String(req.Trailer), req.RemoteAddr, req.RequestURI)

	methodRegister := false
	for _, v := range e.allowedMethods.s {
		if v == req.Method {
			methodRegister = true
			break
		}
	}
	if !methodRegister {
		resp.Header().Set("Allow", e.allowedMethods.str)
		http.Error(resp, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}

	if req.URL.Path == "" {
		req.URL.Path = "/index"
		http.Redirect(resp, req, req.URL.String(), http.StatusPermanentRedirect)
		return
	}

	handler, urlParams, tsr := e.r.GetHandler(req.Method, req.URL.Path)
	if handler != nil {
		defer func() {
			if err := recover(); err != nil {
				logs.CtxCritical(req.Context(), "[panic] err=%v, stack:\n%s", err, debug.Stack())
			}
		}()
		(&engineContext{
			req:       req,
			resp:      resp,
			pathParam: urlParams,
			engine:    e,
			handler:   handler,
			curMW:     e.middlewares,
		}).Next()
		return
	}

	if !tsr {
		http.NotFound(resp, req)
		return
	}

	if req.URL.Path[len(req.URL.Path)-1] == '/' {
		req.URL.Path = req.URL.Path[:len(req.URL.Path)-1]
	} else {
		req.URL.Path += "/"
	}
	http.Redirect(resp, req, req.URL.String(), http.StatusPermanentRedirect)
	return
}

type middleware struct {
	handler func(c Context)
	next    *middleware
}

func (e *engine) AppendMiddleware(handler func(c Context)) {
	if e.middlewares == nil {
		e.middlewares = &middleware{handler: handler}
		return
	}
	mw := e.middlewares
	for mw.next != nil {
		mw = mw.next
	}
	mw.next = &middleware{handler: handler}
}

type engineContext struct {
	req       *http.Request
	resp      http.ResponseWriter
	pathParam []router.UrlParam
	engine    *engine
	handler   router.Handler
	curMW     *middleware
}

func (c *engineContext) GetReq() *http.Request {
	return c.req
}

func (c *engineContext) GetResp() http.ResponseWriter {
	return c.resp
}

func (c *engineContext) GetParamParam() []router.UrlParam {
	return c.pathParam
}

// 返回true表示当存在下一个中间件
func (c *engineContext) Next() bool {
	if c.curMW == nil {
		c.handler(c.GetResp(), c.GetReq(), c.GetParamParam())
		return false
	}
	c.curMW.handler(c)
	c.curMW = c.curMW.next
	return true
}
