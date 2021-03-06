/*

   Copyright 2016 Wenhui Shen <www.webx.top>

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.

*/
package mvc

import (
	"fmt"
	"reflect"
	"runtime"
	"strings"
	"time"
	"sync"

	"github.com/webx-top/echo"
	"github.com/webx-top/echo/handler/mvc/static/resource"
	mw "github.com/webx-top/echo/middleware"
	"github.com/webx-top/echo/middleware/render"
	"github.com/webx-top/echo/middleware/render/driver"
)

func NewModule(name string, domain string, s *Application, middlewares ...interface{}) (a *Module) {
	a = &Module{
		Application:        s,
		Name:               name,
		Domain:             domain,
		wrappers:           make(map[string]*Wrapper),
		cachedHandlerNames: make(map[string]string),
		Middlewares:        middlewares,
		Config:             &ModuleConfig{},
		lock:               &sync.RWMutex{},
	}
	if s.Renderer != nil {
		a.Renderer = s.Renderer
	}
	if len(a.Domain) == 0 {
		var prefix string
		if name != s.RootModuleName {
			prefix = `/` + name
			a.Dir = prefix + `/`
		} else {
			a.Dir = `/`
		}
		a.URL = a.Dir
		if s.URL != `/` {
			a.URL = strings.TrimSuffix(s.URL, `/`) + a.URL
		}
		a.Group = s.Core.Group(prefix)
		a.Group.Use(middlewares...)
	} else {
		e := echo.NewWithContext(s.ContextCreator)
		e.SetRenderer(s.Core.Renderer())
		e.SetHTTPErrorHandler(s.Core.HTTPErrorHandler())
		e.Pre(s.DefaultPreMiddlewares...)
		e.Use(s.DefaultMiddlewares...)
		e.Use(middlewares...)
		a.Handler = e
		scheme := `http`
		if s.SessionOptions.Secure {
			scheme = `https`
		}
		a.URL = scheme + `://` + a.Domain + `/`
		a.Dir = `/`
	}
	if s.RootModuleName == name {
		a.Installed = time.Now().Unix()
	}
	return
}

type Module struct {
	*Application       `json:"-" xml:"-"`
	Group              *echo.Group   `json:"-" xml:"-"`
	Handler            *echo.Echo    `json:"-" xml:"-"` //指定域名时有效
	PreMiddlewares     []interface{} `json:"-" xml:"-"`
	Middlewares        []interface{} `json:"-" xml:"-"`
	Renderer           driver.Driver `json:"-" xml:"-"`
	Resource           *resource.Static
	Name               string
	Domain             string
	wrappers           map[string]*Wrapper
	cachedHandlerNames map[string]string
	URL                string
	Dir                string

	// 模块附加信息
	Disabled    int64  // 禁用时间戳，为0时为启用状态
	Installed   int64  // 安装时间戳，为0时为未安装
	Expired     int64  // 过期时间戳，为0时为永不过期
	Author      string // 作者名称
	Website     string // 作者网址
	Email       string // 作者邮箱
	Description string // 简介
	Config      ModuleConfiger

	// 安装和卸载逻辑
	Install   func() error `json:"-" xml:"-"`
	Uninstall func() error `json:"-" xml:"-"`

	lock *sync.RWMutex
}

func (a *Module) Valid() error {
	if a.Installed == 0 {
		return ErrModuleHasNotBeenInstalled
	}
	if a.Disabled > 0 {
		return ErrModuleHasBeenDisabled
	}
	if a.Expired < time.Now().Unix() {
		return ErrModuleHasExpired
	}
	return nil
}

// Register 注册路由：module.Register(`/index`,Index.Index,"GET","POST")
func (a *Module) Register(p string, v interface{}, methods ...string) *Module {
	if len(methods) < 1 {
		methods = append(methods, "GET")
	}
	a.Application.URLBuilder.Set(v)
	h := a.Core.ValidHandler(v)
	a.Router().Match(methods, p, func(ctx echo.Context) error {
		if c, y := ctx.(Initer); y {
			if err := c.Init(ctx); err != nil {
				return err
			}
		}
		return h.Handle(ctx)
	})
	return a
}

func (a *Module) Router() echo.ICore {
	if a.Group != nil {
		return a.Group
	}
	return a.Handler
}

// C 获取控制器
func (a *Module) C(name string) (c interface{}) {
	if wp, ok := a.wrappers[name]; ok {
		c = wp.Controller
	}
	return
}

// Wrapper 获取封装器
func (a *Module) Wrapper(name string) (wp *Wrapper) {
	wp, _ = a.wrappers[name]
	return
}

// AddHandler 登记控制器
func (a *Module) AddHandler(c interface{}) *Wrapper {
	name := fmt.Sprintf("%T", c) //example: *controller.Index
	if len(name) > 0 && name[0] == '*' {
		name = name[1:]
	}
	wr := &Wrapper{
		Controller:    c,
		RouteRegister: a.Router(),
		Module:        a,
	}
	if _, ok := c.(Initer); ok {
		_, wr.hasBefore = c.(Before)
		_, wr.hasAfter = c.(After)
		_, wr.hasMain = c.(Main)
	} else {
		if hf, ok := c.(BeforeHandler); ok {
			wr.beforeHandler = hf.Before
		}
		if hf, ok := c.(AfterHandler); ok {
			wr.afterHandler = hf.After
		}
	}
	//controller.Index
	a.wrappers[name] = wr
	return wr
}

// Add 批量注册控制器路由
func (a *Module) Add(args ...interface{}) *Module {
	for _, c := range args {
		a.AddHandler(c).Auto()
	}
	return a
}

// Pre 前置中间件
func (a *Module) Pre(middleware ...interface{}) {
	if a.Handler != nil {
		a.Handler.Pre(middleware...)
	} else {
		a.Application.Core.Pre(middleware...)
	}
	a.PreMiddlewares = append(middleware, a.PreMiddlewares...)

}

// Use 中间件
func (a *Module) Use(middleware ...interface{}) {
	a.Router().Use(middleware...)
	a.Middlewares = append(a.Middlewares, middleware...)
}

// InitRenderer 初始化渲染接口(用于单独对app指定renderer，如不指定，默认会使用Server中Renderer)
func (a *Module) InitRenderer(conf *render.Config, funcMap map[string]interface{}) *Module {
	a.Renderer, a.Resource = a.Application.NewRenderer(conf, a, funcMap)
	if a.Handler != nil {
		a.Handler.SetRenderer(a.Renderer)
		//为了支持域名绑定和解除绑定，此处使用路由Hander的方式服务静态资源文件
		a.Resource.Wrapper(a.Handler)
	} else {
		a.Group.Get(strings.TrimPrefix(a.Resource.Path, `/`+a.Name)+`/*`, a.Resource)
	}
	a.Use(mw.SimpleFuncMap(funcMap))
	return a
}

// SafelyCall invokes `function` in recover block
func (a *Module) SafelyCall(fn reflect.Value, args []reflect.Value) (resp []reflect.Value, err error) {
	defer func() {
		if e := recover(); e != nil {
			resp = nil
			panicErr := echo.NewPanicError(e, nil)
			panicErr.SetDebug(a.Application.Core.Debug())
			content := fmt.Sprintf(`Handler crashed with error: %v`, e)
			for i := 1; ; i++ {
				pc, file, line, ok := runtime.Caller(i)
				if !ok {
					break
				}
				t := &echo.Trace{
					File: file,
					Line: line,
					Func: runtime.FuncForPC(pc).Name(),
				}
				panicErr.AddTrace(t)
				content += "\n" + fmt.Sprintf(`%v:%v`, file, line)
			}
			panicErr.SetErrorString(content)
			a.Application.Core.Logger().Error(panicErr)
			err = panicErr
		}
	}()
	if fn.Type().NumIn() > 0 {
		return fn.Call(args), err
	}
	return fn.Call(nil), err
}

func (a *Module) ClearCachedHandlerNames() {
	a.cachedHandlerNames = map[string]string{}
}

func (a *Module) Commit() {
	if a.Group != nil {
		return
	}
	a.Handler.Commit()
}

// ExecAction 执行Action的通用方式
func (a *Module) ExecAction(action string, t reflect.Type, v reflect.Value, c echo.Context) (err error) {
	a.lock.Lock()
	k := t.PkgPath() + `.` + t.Name() + `.` + action + `_` + c.Method() + `_` + c.Format()
	var m reflect.Value
	if methodName, ok := a.cachedHandlerNames[k]; ok {
		m = v.MethodByName(methodName)
	} else {
		format := strings.ToUpper(c.Format())
		actions := []string{
			action + `_` + c.Method() + `_` + format,
			action + `_` + c.Method(),
			action + `_` + format,
			action,
		}
		var valid bool
		for _, act := range actions {
			methodName = act
			m = v.MethodByName(act)
			valid = m.IsValid()
			if valid {
				break
			}
		}
		if !valid {
			err = echo.NewHTTPError(404, `invalid action: `+t.Name()+`#`+action)
			a.lock.Unlock()
			return
		}
		a.cachedHandlerNames[k] = methodName
	}
	a.lock.Unlock()
	r, err := a.SafelyCall(m, []reflect.Value{})
	if err != nil {
		return err
	}
	size := len(r)
	switch size {
	case 1:
		rs := r[0].Interface()
		if err, ok := rs.(error); ok {
			return err
		}
	}
	return nil
}
