// Copyright 2018 gf Author(https://gitee.com/johng/gf). All Rights Reserved.
//
// This Source Code Form is subject to the terms of the MIT License.
// If a copy of the MIT was not distributed with this file,
// You can obtain one at https://gitee.com/johng/gf.

package gproc

import (
    "os"
    "fmt"
    "gitee.com/johng/gf/g/os/glog"
    "gitee.com/johng/gf/g/os/gfile"
    "gitee.com/johng/gf/g/os/gflock"
    "gitee.com/johng/gf/g/util/gconv"
    "gitee.com/johng/gf/g/os/gfsnotify"
    "gitee.com/johng/gf/g/container/gqueue"
    "gitee.com/johng/gf/g/encoding/gbinary"
    "gitee.com/johng/gf/g/os/gtime"
)

const (
    // 由于子进程的temp dir有可能会和父进程不一致(特别是windows下)，影响进程间通信，这里统一使用环境变量设置
    gPROC_TEMP_DIR_ENV_KEY = "gproc.tempdir"
)

// 当前进程的文件锁
var commLocker  = gflock.New(fmt.Sprintf("%d.lock", os.Getpid()))
// 进程通信消息队列
var commQueue   = gqueue.New()

// TCP通信数据结构定义
type Msg struct {
    Pid  int    // PID，哪个进程发送的消息
    Data []byte // 参数，消息附带的参数
}

// 进程管理/通信初始化操作
func init() {
    path := getCommFilePath(os.Getpid())
    if !gfile.Exists(path) {
        if err := gfile.Create(path); err != nil {
            glog.Error(err)
        }
    } else {
        // 初始化时读取已有数据(文件修改时间在10秒以内)
        if gtime.Second() - gfile.MTime(path) < 10 {
            checkCommBuffer(path)
        }
    }
    // 文件事件监听，如果通信数据文件有任何变化，读取文件并添加到消息队列
    err := gfsnotify.Add(path, func(event *gfsnotify.Event) {
        checkCommBuffer(path)
    })
    if err != nil {
        glog.Error(err)
    }
}

// 手动检查进程通信消息，如果存在消息曾推送到进程消息队列
func checkCommBuffer(path string) {
    commLocker.Lock()
    buffer := gfile.GetBinContents(path)
    if len(buffer) > 0 {
        os.Truncate(path, 0)
    }
    commLocker.UnLock()
    if len(buffer) > 0 {
        for _, v := range bufferToMsgs(buffer) {
            commQueue.PushBack(v)
        }
    }
}

// 获取其他进程传递到当前进程的消息包，阻塞执行
func Receive() *Msg {
    if v := commQueue.PopFront(); v != nil {
        return v.(*Msg)
    }
    return nil
}

// 向指定gproc进程发送数据
// 数据格式：总长度(32bit) | PID(32bit) | 校验(32bit) | 参数(变长)
func Send(pid int, data interface{}) error {
    buffer := gconv.Bytes(data)
    b := make([]byte, 0)
    b  = append(b, gbinary.EncodeInt32(int32(len(buffer) + 12))...)
    b  = append(b, gbinary.EncodeInt32(int32(os.Getpid()))...)
    b  = append(b, gbinary.EncodeUint32(checksum(buffer))...)
    b  = append(b, buffer...)
    l := gflock.New(fmt.Sprintf("%d.lock", pid))
    l.Lock()
    err := gfile.PutBinContentsAppend(getCommFilePath(pid), b)
    l.UnLock()
    return err
}

// 获取指定进程的通信文件地址
func getCommFilePath(pid int) string {
    tempDir := os.Getenv("gproc.tempdir")
    if tempDir == "" {
        tempDir = gfile.TempDir()
    }
    return tempDir + gfile.Separator + "gproc" + gfile.Separator + gconv.String(pid)
}

// 数据解包，防止黏包
func bufferToMsgs(buffer []byte) []*Msg {
    s    := 0
    msgs := make([]*Msg, 0)
    for s < len(buffer) {
        length := gbinary.DecodeToInt(buffer[s : s + 4])
        if length < 0 || length > len(buffer) {
            s++
            continue
        }
        checksum1 := gbinary.DecodeToUint32(buffer[s + 8 : s + 12])
        checksum2 := checksum(buffer[s + 12 : s + length])
        if checksum1 != checksum2 {
            s++
            continue
        }
        msgs = append(msgs, &Msg {
            Pid  : gbinary.DecodeToInt(buffer[s + 4 : s + 8]),
            Data : buffer[s + 12 : s + length],
        })
        s += length
    }
    return msgs
}

// 常见的二进制数据校验方式，生成校验结果
func checksum(buffer []byte) uint32 {
    var checksum uint32
    for _, b := range buffer {
        checksum += uint32(b)
    }
    return checksum
}