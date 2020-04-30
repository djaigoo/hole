package main

import (
    "encoding/json"
    "github.com/djaigoo/hole/src/dao"
    "github.com/djaigoo/logkit"
    "net/http"
    "sort"
    "strconv"
)

func errReport(name string) {
    if err := recover(); err != nil {
        logkit.Errorf("%s panic %#v", name, err)
    }
}

func openPProf() {
    http.HandleFunc("/debug/pprof/", Index)
    http.HandleFunc("/debug/pprof/cmdline", Cmdline)
    http.HandleFunc("/debug/pprof/profile", Profile)
    http.HandleFunc("/debug/pprof/symbol", Symbol)
    http.HandleFunc("/debug/pprof/trace", Trace)
}

func adminServer(port int, addr, password string) {
    logkit.Infof("start admin server")
    dao.RedisDao = dao.NewRedisDao(addr, password)
    http.HandleFunc("/connects", connects)
    err := http.ListenAndServe(":"+strconv.Itoa(port), nil)
    if err != nil {
        logkit.Error(err.Error())
        return
    }
}

func connects(w http.ResponseWriter, r *http.Request) {
    defer errReport("connects")
    type response struct {
        List []string `json:"list"`
    }
    list, err := dao.RedisDao.GetConnects()
    if err != nil {
        logkit.Error(err.Error())
        return
    }
    sort.Strings(list)
    resp := &response{List: list}
    data, err := json.Marshal(resp)
    if err != nil {
        logkit.Error(err.Error())
        return
    }
    w.Write(data)
}
