package easyserver

import (
	"fmt"
	"net/http"
	"runtime/debug"
	"sort"
	"strings"

	"github.com/gogokit/logs"
	"github.com/gogokit/router"
	"github.com/gogokit/tostr"
)

type Group struct {
	RootPath    string
	Middlewares []func(c Context)
	Children    []Node
}

type Node struct {
	Method      string
	Path        string
	Middlewares []func(c Context)
	Handler     func(c Context)
}

type Engine interface {
	Register(node Node)
	RegisterGroup(group Group)
	RunHttp(port int) error
	RunHttps(port int, certFile, keyFile string) error
}

type Context interface {
	GetReq() *http.Request
	GetResp() http.ResponseWriter
	GetParamParam() []router.UrlParam
	GetMatchPath() string
	Next() bool
}

func New() Engine {
	return &engine{
		r: router.New(),
	}
}

type engine struct {
	r              router.Router
	allowedMethods struct {
		s   []string
		str string
	}
}

type routerValue struct {
	middlewares []func(c Context)
	matchPath   string
}

func (e *engine) Register(node Node) {
	node.Middlewares = append(node.Middlewares, node.Handler)
	for _, v := range node.Middlewares {
		if v == nil {
			panic("middleware or handle of a node is nil")
		}
	}

	e.r.Register(node.Method, node.Path, &routerValue{
		middlewares: node.Middlewares,
		matchPath:   node.Path,
	})
	for _, v := range e.allowedMethods.s {
		if v == node.Method {
			return
		}
	}
	e.allowedMethods.s = append(e.allowedMethods.s, node.Method)
	sort.Strings(e.allowedMethods.s)
	e.allowedMethods.str = strings.Join(e.allowedMethods.s, ",")
}

func (e *engine) RegisterGroup(group Group) {
	for _, v := range group.Children {
		v.Middlewares = append(group.Middlewares, v.Middlewares...)
		v.Path = group.RootPath + v.Path
		e.Register(v)
	}
}

func (e *engine) RunHttp(port int) error {
	return http.ListenAndServe(":"+fmt.Sprintf("%d", port), e)
}

func (e *engine) RunHttps(port int, certFile, keyFile string) error {
	return http.ListenAndServeTLS(":"+fmt.Sprintf("%d", port), certFile, keyFile, e)
}

func (e *engine) ServeHTTP(resp http.ResponseWriter, req *http.Request) {
	logId := logs.GenLogId()
	req = req.WithContext(logs.CtxWithLogId(req.Context(), logId))
	defer func() {
		resp.Header().Set(string(logs.LogIdContextKey), logId)
		logs.CtxTrace(req.Context(), "[EasyServer] Resp=%v", tostr.String(&struct {
			Header interface{}
		}{
			Header: resp.Header(),
		}))
	}()

	logs.CtxTrace(req.Context(), "[EasyServer] Req=%v", tostr.String(&struct {
		Method           interface{}
		URL              interface{}
		Proto            interface{}
		ProtoMajor       interface{}
		ProtoMinor       interface{}
		Header           interface{}
		Host             interface{}
		Form             interface{}
		PostForm         interface{}
		MultipartForm    interface{}
		Trailer          interface{}
		RemoteAddr       interface{}
		RequestURI       interface{}
		ContentLength    interface{}
		TransferEncoding interface{}
	}{
		Method:           req.Method,
		URL:              req.URL,
		Proto:            req.Proto,
		ProtoMajor:       req.ProtoMajor,
		ProtoMinor:       req.ProtoMinor,
		Header:           req.Header,
		Host:             req.Host,
		Form:             req.Form,
		PostForm:         req.PostForm,
		MultipartForm:    req.MultipartForm,
		Trailer:          req.Trailer,
		RemoteAddr:       req.RemoteAddr,
		RequestURI:       req.RequestURI,
		ContentLength:    req.ContentLength,
		TransferEncoding: req.TransferEncoding,
	}))

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
		req.URL.Path = "/"
		http.Redirect(resp, req, req.URL.String(), http.StatusPermanentRedirect)
		return
	}

	value, urlParams, redirect := e.r.Lookup(req.Method, req.URL.Path)
	if value != nil {
		h := value.(*routerValue)
		defer func() {
			if err := recover(); err != nil {
				logs.CtxCritical(req.Context(), "[EasyServer] panic in handler, err=%v, stack=\n%s", err, debug.Stack())
			}
		}()
		logs.CtxTrace(req.Context(), "[EasyServer] mathPath=%v, pathParam=%v", tostr.String(h.matchPath), tostr.String(urlParams))
		(&reqContext{
			req:         req,
			resp:        resp,
			pathParam:   urlParams,
			middlewares: h.middlewares,
			matchPath:   h.matchPath,
		}).Next()
		return
	}

	if !redirect {
		http.NotFound(resp, req)
		return
	}

	if req.URL.Path[len(req.URL.Path)-1] == '/' {
		req.URL.Path = req.URL.Path[:len(req.URL.Path)-1]
	} else {
		req.URL.Path += "/"
	}
	http.Redirect(resp, req, req.URL.String(), http.StatusPermanentRedirect)
}

type reqContext struct {
	req         *http.Request
	resp        http.ResponseWriter
	pathParam   []router.UrlParam
	middlewares []func(c Context)
	curMW       int
	matchPath   string
}

func (c *reqContext) GetReq() *http.Request {
	return c.req
}

func (c *reqContext) GetResp() http.ResponseWriter {
	return c.resp
}

func (c *reqContext) GetParamParam() []router.UrlParam {
	return c.pathParam
}

func (c *reqContext) GetMatchPath() string {
	return c.matchPath
}

// 返回true表示存在下一个中间件
func (c *reqContext) Next() bool {
	if c.curMW >= len(c.middlewares) {
		return false
	}
	c.curMW++
	c.middlewares[c.curMW-1](c)
	return true
}
