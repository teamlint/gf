// Copyright 2017 gf Author(https://gitee.com/johng/gf). All Rights Reserved.
//
// This Source Code Form is subject to the terms of the MIT License.
// If a copy of the MIT was not distributed with this file,
// You can obtain one at https://gitee.com/johng/gf.

package ghttp

import (
    "sync"
    "errors"
    "strings"
    "reflect"
    "net/http"
    "gitee.com/johng/gf/g/os/glog"
    "gitee.com/johng/gf/g/os/gcache"
    "gitee.com/johng/gf/g/container/gmap"
    "gitee.com/johng/gf/g/container/gtype"
    "gitee.com/johng/gf/g/container/gqueue"
)

const (
    gHTTP_METHODS             = "GET,PUT,POST,DELETE,PATCH,HEAD,CONNECT,OPTIONS,TRACE"
    gDEFAULT_SERVER           = "default"
    gDEFAULT_DOMAIN           = "default"
    gDEFAULT_METHOD           = "ALL"
    gDEFAULT_COOKIE_PATH      = "/"           // 默认path
    gDEFAULT_COOKIE_MAX_AGE   = 86400*365     // 默认cookie有效期(一年)
    gDEFAULT_SESSION_MAX_AGE  = 600           // 默认session有效期(600秒)
    gDEFAULT_SESSION_ID_NAME  = "gfsessionid" // 默认存放Cookie中的SessionId名称
)

// http server结构体
type Server struct {
    hmmu             sync.RWMutex             // handler互斥锁
    hhmu             sync.RWMutex             // hooks互斥锁
    hmcmu            sync.RWMutex             // handlerCache互斥锁
    hhcmu            sync.RWMutex             // hooksCache互斥锁
    name             string                   // 服务名称，方便识别
    server           http.Server              // 底层http server对象
    config           ServerConfig             // 配置对象
    status           int8                     // 当前服务器状态(0：未启动，1：运行中)
    methodsMap       map[string]bool          // 所有支持的HTTP Method(初始化时自动填充)
    handlerMap       HandlerMap               // 所有注册的回调函数(静态匹配)
    handlerTree      map[string]interface{}   // 所有注册的回调函数(动态匹配，树型+链表优先级匹配)
    hooksTree        map[string]interface{}   // 所有注册的事件回调函数(动态匹配，树型+链表优先级匹配)
    handlerCache     *gcache.Cache            // 服务注册路由内存缓存
    hooksCache       *gcache.Cache            // 回调事件注册路由内存缓存
    servedCount      *gtype.Int               // 已经服务的请求数(4-8字节，不考虑溢出情况)
    cookieMaxAge     *gtype.Int               // Cookie有效期
    sessionMaxAge    *gtype.Int               // Session有效期
    sessionIdName    *gtype.String            // SessionId名称
    cookies          *gmap.IntInterfaceMap    // 当前服务器正在服务(请求正在执行)的Cookie(每个请求一个Cookie对象)
    sessions         *gcache.Cache            // Session内存缓存
    closeQueue       *gqueue.Queue            // 请求结束的关闭队列(存放的是需要异步关闭处理的*Request对象)
    logPath          *gtype.String            // 存放日志的目录路径
    errorLogEnabled  *gtype.Bool              // 是否开启error log
    accessLogEnabled *gtype.Bool              // 是否开启access log
    logger           *glog.Logger             // 日志对象
    logHandler       *gtype.Interface         // 自定义的日志处理回调方法
}

// 域名、URI与回调函数的绑定记录表
type HandlerMap  map[string]*HandlerItem

// http回调函数注册信息
type HandlerItem struct {
    ctype    reflect.Type // 控制器类型
    fname    string       // 回调方法名称
    faddr    HandlerFunc  // 准确的执行方法内存地址(与以上两个参数二选一)
    uri      string       // 注册时的pattern - uri
    method   string       // 注册时的pattern - method
    domain   string       // 注册时的pattern - domain
    priority int          // 优先级，用于链表排序，值越大优先级越高
}

// http注册函数
type HandlerFunc func(*Request)

// Server表，用以存储和检索名称与Server对象之间的关联关系
var serverMapping = gmap.NewStringInterfaceMap()

// 获取/创建一个默认配置的HTTP Server(默认监听端口是80)
// 单例模式，请保证name的唯一性
func GetServer(names...string) (*Server) {
    name := gDEFAULT_SERVER
    if len(names) > 0 {
        name = names[0]
    }
    if s := serverMapping.Get(name); s != nil {
        return s.(*Server)
    }
    s := &Server {
        name             : name,
        methodsMap       : make(map[string]bool),
        handlerMap       : make(HandlerMap),
        handlerTree      : make(map[string]interface{}),
        hooksTree        : make(map[string]interface{}),
        handlerCache     : gcache.New(),
        hooksCache       : gcache.New(),
        cookies          : gmap.NewIntInterfaceMap(),
        sessions         : gcache.New(),
        cookieMaxAge     : gtype.NewInt(gDEFAULT_COOKIE_MAX_AGE),
        sessionMaxAge    : gtype.NewInt(gDEFAULT_SESSION_MAX_AGE),
        sessionIdName    : gtype.NewString(gDEFAULT_SESSION_ID_NAME),
        servedCount      : gtype.NewInt(),
        closeQueue       : gqueue.New(),
        logPath          : gtype.NewString(),
        accessLogEnabled : gtype.NewBool(),
        errorLogEnabled  : gtype.NewBool(),
        logger           : glog.New(),
        logHandler       : gtype.NewInterface(),
    }
    s.logger.SetBacktraceSkip(4)
    // 设置路由解析缓存上限，使用LRU进行缓存淘汰
    s.hooksCache.SetCap(10000)
    s.handlerCache.SetCap(10000)
    for _, v := range strings.Split(gHTTP_METHODS, ",") {
        s.methodsMap[v] = true
    }
    s.SetConfig(defaultServerConfig)
    serverMapping.Set(name, s)
    return s
}

// 阻塞执行监听
func (s *Server) Run() error {
    if s.status == 1 {
        return errors.New("server is already running")
    }

    // 底层http server配置
    if s.config.Handler == nil {
        s.config.Handler = http.HandlerFunc(s.defaultHttpHandle)
    }
    // 底层http server初始化
    s.server  = http.Server {
        Addr           : s.config.Addr,
        Handler        : s.config.Handler,
        ReadTimeout    : s.config.ReadTimeout,
        WriteTimeout   : s.config.WriteTimeout,
        IdleTimeout    : s.config.IdleTimeout,
        MaxHeaderBytes : s.config.MaxHeaderBytes,
    }
    // 开启异步处理队列处理循环
    s.startCloseQueueLoop()
    // 执行端口监听
    if err := s.server.ListenAndServe(); err != nil {
        glog.Error(err)
        return err
    }
    s.status = 1
    return nil
}

// 清空当前的handlerCache
func (s *Server) clearHandlerCache() {
    s.hmcmu.Lock()
    defer s.hmcmu.Unlock()
    s.handlerCache.Close()
    s.handlerCache = gcache.New()
}

// 清空当前的hooksCache
func (s *Server) clearHooksCache() {
    s.hhcmu.Lock()
    defer s.hhcmu.Unlock()
    s.hooksCache.Close()
    s.hooksCache = gcache.New()
}