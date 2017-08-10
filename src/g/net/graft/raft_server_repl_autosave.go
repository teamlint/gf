// 数据同步需要注意的是：
// leader只有在通知完所有follower更新完数据之后，自身才会进行数据更新
// 因此leader
package graft

import (
    "g/encoding/gjson"
    "time"
    "log"
    "g/core/types/gmap"
    "g/os/gfile"
    "g/util/gtime"
)

// 保存日志数据
func (n *Node) saveLogEntry(entry LogEntry) {
    switch entry.Act {
        case gMSG_REPL_SET:
            log.Println("setting log entry", entry)
            for k, v := range entry.Items.(map[string]interface{}) {
                n.KVMap.Set(k, v.(string))
            }


        case gMSG_REPL_REMOVE:
            log.Println("removing log entry", entry)
            for _, v := range entry.Items.([]interface{}) {
                n.KVMap.Remove(v.(string))
            }

    }
    n.setLastLogId(entry.Id)
}

// 日志自动保存处理
func (n *Node) logAutoSavingHandler() {
    t := gtime.Millisecond()
    for {
        // 当日志列表的最新ID与保存的ID不相等，或者超过超时时间
        if n.getLastLogId() != n.getLastSavedLogId() || gtime.Millisecond() - t > gLOG_REPL_AUTOSAVE_INTERVAL {
            //log.Println("saving data to file")
            n.saveData()
            t = gtime.Millisecond()
        } else {
            time.Sleep(100 * time.Millisecond)
        }
    }
}

// 保存数据到磁盘
func (n *Node) saveData() {
    data := SaveInfo {
        LastLogId : n.getLastLogId(),
        Peers     : *n.Peers.Clone(),
        DataMap   : *n.KVMap.Clone(),
    }
    content := gjson.Encode(&data)
    gfile.PutContents(n.getDataFilePath(), *content)
    n.setLastSavedLogId(n.getLastLogId())
}

// 从物理化文件中恢复变量
func (n *Node) restoreData() {
    path := n.getDataFilePath()
    if gfile.Exists(path) {
        content := gfile.GetContents(path)
        if content != nil {
            log.Println("restore data from file:", path)
            var data = SaveInfo {
                Peers   : make(map[string]interface{}),
                DataMap : make(map[string]string),
            }
            content := string(content)
            if gjson.DecodeTo(&content, &data) == nil {
                dataMap := gmap.NewStringStringMap()
                peerMap := gmap.NewStringInterfaceMap()
                infoMap := make(map[string]NodeInfo)
                gjson.DecodeTo(gjson.Encode(data.Peers), &infoMap)
                dataMap.BatchSet(data.DataMap)
                for k, v := range infoMap {
                    peerMap.Set(k, v)
                }
                n.setLastLogId(data.LastLogId)
                n.setLastSavedLogId(data.LastLogId)
                n.setPeers(peerMap)
                n.setKVMap(dataMap)
            }
        }
    } else {
        //log.Println("no data file found at", path)
    }
}

// 使用logentry数组更新本地的日志列表
func (n *Node) updateFromLogEntriesJson(jsonContent *string) error {
    array := make([]LogEntry, 0)
    err   := gjson.DecodeTo(jsonContent, &array)
    if err != nil {
        log.Println(err)
        return err
    }
    if array != nil && len(array) > 0 {
        for _, v := range array {
            if v.Id > n.getLastLogId() {
                n.saveLogEntry(v)
            }
        }
    }
    return nil
}


