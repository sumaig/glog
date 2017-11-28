# 简介
glog是基于beego架构中的log模块修改而来，做了些优化以及功能的删减。

# 如何使用

## 通用方式
首先引入包:

import (
    "github.com/sumaig/glog"
)

然后添加输出引擎（log 支持同时输出到多个引擎），这里我们以 console 为例，第一个参数是引擎名（包括：console、file、conn、smtp、es、multifile）

logs.SetLogger("console")
添加输出引擎也支持第二个参数,用来表示配置信息，详细的配置请看下面介绍：

logs.SetLogger(logs.AdapterFile,\`{"filename":"project.log","level":7,"maxlines":0,"maxsize":0,"daily":true,"maxdays":10}\`)
然后我们就可以在我们的逻辑中开始任意的使用了：

```
package main

import (
    "github.com/sumaig/glog"
)

func main() {
    //an official log.Logger
    l := glog.GetLogger()
    l.Println("this is a message of http")
    //an official log.Logger with prefix ORM
    glog.GetLogger("ORM").Println("this is a message of orm")
    glog.Debug("my book is bought in the year of ", 2016)
    glog.Info("this %s cat is %v years old", "yellow", 3)
    glog.Warn("json is a type of kv like", map[string]int{"key": 2016})
    glog.Error(1024, "is a very", "good game")
    glog.Critical("oh,crash")
}
```

## 多实例
一般推荐使用通用方式进行日志，但依然支持单独声明来使用独立的日志
```
    package main
    import (
        "github.com/sumaig/glog"
    )
    func main() {
        log := glog.NewLogger()
        log.SetLogger(logs.AdapterConsole)
        log.Debug("this is a debug message")
    }
```

## 输出文件名和行号
日志默认不输出调用的文件名和文件行号,如果你期望输出调用的文件名和文件行号,可以如下设置

glog.EnableFuncCallDepth(true)
> 开启传入参数 true,关闭传入参数 false,默认是关闭的.

glog.SetLogFuncCallDepth(3)
> 如果你的应用自己封装了调用 log 包,那么需要设置 SetLogFuncCallDepth,默认是 2,也就是直接调用的层级,如果你封装了多层,那么需要根据自己的需求进行调整.

glog.Async()
> 为了提升性能, 可以设置异步输出

glog.Async(1e3)
> 异步输出允许设置缓冲 chan 的大小

## 引擎配置设置
### console

可以设置输出的级别，或者不设置保持默认，默认输出到 os.Stdout：

glog.SetLogger(logs.AdapterConsole, \`{"level":1}\`)

### file

设置的例子如下所示：

glog.SetLogger(logs.AdapterFile, \`{"filename":"test.log"}\`)

主要的参数如下说明：

filename 保存的文件名

maxlines 每个文件保存的最大行数，默认值 1000000

maxsize 每个文件保存的最大尺寸，默认值是 1 << 28, //256 MB

daily 是否按照每天 logrotate，默认是 true

maxdays 文件最多保存多少天，默认保存 7 天

rotate 是否开启 logrotate，默认是 true

level 日志保存的时候的级别，默认是 Trace 级别

perm 日志文件权限

### multifile

glog.SetLogger(glog.AdapterMultiFile, \`{"filename":"test.log","separate":["emergency", "alert", "critical", "error", "warning", "notice", "info", "debug"]}\`)

主要的参数如下说明(除 separate 外,均与file相同)：

filename 保存的文件名

maxlines 每个文件保存的最大行数，默认值 1000000

maxsize 每个文件保存的最大尺寸，默认值是 1 << 28, //256 MB

daily 是否按照每天 logrotate，默认是 true

maxdays 文件最多保存多少天，默认保存 7 天

rotate 是否开启 logrotate，默认是 true

level 日志保存的时候的级别，默认是 Trace 级别

perm 日志文件权限

separate 需要单独写入文件的日志级别,设置后命名类似 test.error.log