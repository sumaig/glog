package glog

import (
	"fmt"
	"os"
	"path"
	"runtime"
	"strconv"
	"sync"
	"time"
	"log"
	"strings"
)

// RFC5424 log message levels.
const (
	LevelEmergency = iota
	LevelAlert
	LevelCritical
	LevelError
	LevelWarning
	LevelNotice
	LevelInformational
	LevelDebug
)

// levelGoLogger is defined to implement log.Logger
// the real log level will be LevelEmergency
const levelLoggerImpl = -1

// Name for adapter with gloger official support
const (
	AdapterConsole   = "console"
	AdapterFile      = "file"
	AdapterMultiFile = "multifile"
)

// Legacy log level constants to ensure backwards compatibility.
const (
	LevelInfo  = LevelInformational
	LevelTrace = LevelDebug
	LevelWarn  = LevelWarning
)

type newLoggerFunc func() Logger

// Logger defines the behavior of a log provider.
type Logger interface {
	Init(config string) error
	WriteMsg(when time.Time, msg string, level int) error
	Destroy()
	Flush()
}

var adapters = make(map[string]newLoggerFunc)
var levelPrefix = [LevelDebug + 1]string{"[M] ", "[A] ", "[C] ", "[E] ", "[W] ", "[N] ", "[I] ", "[D] "}

// Register makes a log provide available by the provided name.
// If Register is called twice with the same name or if driver is nil,
// it panics.
func Register(name string, log newLoggerFunc) {
	if log == nil {
		panic("logs: Register provide is nil")
	}
	if _, dup := adapters[name]; dup {
		panic("logs: Register called twice for provider " + name)
	}
	adapters[name] = log
}

// GoLogger is default logger.
// it can contain several providers and log message into all providers.
type GoLogger struct {
	lock                sync.Mutex
	level               int
	init                bool
	enableFuncCallDepth bool
	loggerFuncCallDepth int
	asynchronous        bool
	msgChanLen          int64
	msgChan             chan *logMsg
	signalChan          chan string
	wg                  sync.WaitGroup
	outputs             []*nameLogger
}

const defaultAsyncMsgLen = 1e3

type nameLogger struct {
	Logger
	name string
}

type logMsg struct {
	level int
	msg   string
	when  time.Time
}

var logMsgPool *sync.Pool

// NewLogger returns a new GoLogger.
// channelLen means the number of messages in chan(used where asynchronous is true).
// if the buffering chan is full, logger adapters write to file or other way.
func NewLogger(channelLens ...int64) *GoLogger {
	g := new(GoLogger)
	g.level = LevelDebug
	g.loggerFuncCallDepth = 2
	g.msgChanLen = append(channelLens, 0)[0]
	if g.msgChanLen <= 0 {
		g.msgChanLen = defaultAsyncMsgLen
	}
	g.signalChan = make(chan string, 1)
	g.setLogger(AdapterConsole)
	return g
}

// Async set the log to asynchronous and start the goroutine
func (g *GoLogger) Async(msgLen ...int64) *GoLogger {
	g.lock.Lock()
	defer g.lock.Unlock()
	if g.asynchronous {
		return g
	}
	g.asynchronous = true
	if len(msgLen) > 0 && msgLen[0] > 0 {
		g.msgChanLen = msgLen[0]
	}
	g.msgChan = make(chan *logMsg, g.msgChanLen)
	logMsgPool = &sync.Pool{
		New: func() interface{} {
			return &logMsg{}
		},
	}
	g.wg.Add(1)
	go g.startLogger()
	return g
}

// SetLogger provides a given logger adapter into GoLogger with config string.
// config need to be correct JSON as string: {"interval":360}.
func (g *GoLogger) setLogger(adapterName string, configs ...string) error {
	config := append(configs, "{}")[0]
	for _, l := range g.outputs {
		if l.name == adapterName {
			return fmt.Errorf("logs: duplicate adaptername %q (you have set this logger before)", adapterName)
		}
	}

	log, ok := adapters[adapterName]
	if !ok {
		return fmt.Errorf("logs: unknown adaptername %q (forgotten Register?)", adapterName)
	}

	lg := log()
	err := lg.Init(config)
	if err != nil {
		fmt.Fprintln(os.Stderr, "logs.GoLogger.SetLogger: "+err.Error())
		return err
	}
	g.outputs = append(g.outputs, &nameLogger{name: adapterName, Logger: lg})
	return nil
}

// SetLogger provides a given logger adapter into GoLogger with config string.
// config need to be correct JSON as string: {"interval":360}.
func (g *GoLogger) SetLogger(adapterName string, configs ...string) error {
	g.lock.Lock()
	defer g.lock.Unlock()
	if !g.init {
		g.outputs = []*nameLogger{}
		g.init = true
	}
	return g.setLogger(adapterName, configs...)
}

// DelLogger remove a logger adapter in GoLogger.
func (g *GoLogger) DelLogger(adapterName string) error {
	g.lock.Lock()
	defer g.lock.Unlock()
	outputs := []*nameLogger{}
	for _, lg := range g.outputs {
		if lg.name == adapterName {
			lg.Destroy()
		} else {
			outputs = append(outputs, lg)
		}
	}
	if len(outputs) == len(g.outputs) {
		return fmt.Errorf("logs: unknown adaptername %q (forgotten Register?)", adapterName)
	}
	g.outputs = outputs
	return nil
}

func (g *GoLogger) writeToLoggers(when time.Time, msg string, level int) {
	for _, l := range g.outputs {
		err := l.WriteMsg(when, msg, level)
		if err != nil {
			fmt.Fprintf(os.Stderr, "unable to WriteMsg to adapter:%v,error:%v\n", l.name, err)
		}
	}
}

func (g *GoLogger) Write(p []byte) (n int, err error) {
	if len(p) == 0 {
		return 0, nil
	}
	// writeMsg will always add a '\n' character
	if p[len(p)-1] == '\n' {
		p = p[0 : len(p)-1]
	}
	// set levelLoggerImpl to ensure all log message will be write out
	err = g.writeMsg(levelLoggerImpl, string(p))
	if err == nil {
		return len(p), err
	}
	return 0, err
}

func (g *GoLogger) writeMsg(logLevel int, msg string, v ...interface{}) error {
	if !g.init {
		g.lock.Lock()
		g.setLogger(AdapterConsole)
		g.lock.Unlock()
	}

	if len(v) > 0 {
		msg = fmt.Sprintf(msg, v...)
	}
	when := time.Now()
	if g.enableFuncCallDepth {
		_, file, line, ok := runtime.Caller(g.loggerFuncCallDepth)
		if !ok {
			file = "???"
			line = 0
		}
		_, filename := path.Split(file)
		msg = "[" + filename + ":" + strconv.FormatInt(int64(line), 10) + "] " + msg
	}

	//set level info in front of filename info
	if logLevel == levelLoggerImpl {
		// set to emergency to ensure all log will be print out correctly
		logLevel = LevelEmergency
	} else {
		msg = levelPrefix[logLevel] + msg
	}

	if g.asynchronous {
		lm := logMsgPool.Get().(*logMsg)
		lm.level = logLevel
		lm.msg = msg
		lm.when = when
		g.msgChan <- lm
	} else {
		g.writeToLoggers(when, msg, logLevel)
	}
	return nil
}

// SetLevel Set log message level.
// If message level (such as LevelDebug) is higher than logger level (such as LevelWarning),
// log providers will not even be sent the message.
func (g *GoLogger) SetLevel(l int) {
	g.level = l
}

// SetLogFuncCallDepth set log funcCallDepth
func (g *GoLogger) SetLogFuncCallDepth(d int) {
	g.loggerFuncCallDepth = d
}

// GetLogFuncCallDepth return log funcCallDepth for wrapper
func (g *GoLogger) GetLogFuncCallDepth() int {
	return g.loggerFuncCallDepth
}

// EnableFuncCallDepth enable log funcCallDepth
func (g *GoLogger) EnableFuncCallDepth(b bool) {
	g.enableFuncCallDepth = b
}

// start logger chan reading.
// when chan is not empty, write logs.
func (g *GoLogger) startLogger() {
	gameOver := false
	for {
		select {
		case bm := <-g.msgChan:
			g.writeToLoggers(bm.when, bm.msg, bm.level)
			logMsgPool.Put(bm)
		case sg := <-g.signalChan:
			// Now should only send "flush" or "close" to g.signalChan
			g.flush()
			if sg == "close" {
				for _, l := range g.outputs {
					l.Destroy()
				}
				g.outputs = nil
				gameOver = true
			}
			g.wg.Done()
		}
		if gameOver {
			break
		}
	}
}

// Emergency Log EMERGENCY level message.
func (g *GoLogger) Emergency(f interface{}, v ...interface{}) {
	if LevelEmergency > g.level {
		return
	}
	format := formatLog(f, v...)
	g.writeMsg(LevelEmergency, format, v...)
}

// Alert Log ALERT level message.
func (g *GoLogger) Alert(f interface{}, v ...interface{}) {
	if LevelAlert > g.level {
		return
	}
	format := formatLog(f, v...)
	g.writeMsg(LevelAlert, format, v...)
}

// Critical Log CRITICAL level message.
func (g *GoLogger) Critical(f interface{}, v ...interface{}) {
	if LevelCritical > g.level {
		return
	}
	format := formatLog(f, v...)
	g.writeMsg(LevelCritical, format, v...)
}

// Error Log ERROR level message.
func (g *GoLogger) Error(f interface{}, v ...interface{}) {
	if LevelError > g.level {
		return
	}
	format := formatLog(f, v...)
	g.writeMsg(LevelError, format, v...)
}

// Warning Log WARNING level message.
func (g *GoLogger) Warning(f interface{}, v ...interface{}) {
	if LevelWarn > g.level {
		return
	}
	format := formatLog(f, v...)
	g.writeMsg(LevelWarn, format, v...)
}

// Notice Log NOTICE level message.
func (g *GoLogger) Notice(f interface{}, v ...interface{}) {
	if LevelNotice > g.level {
		return
	}
	format := formatLog(f, v...)
	g.writeMsg(LevelNotice, format, v...)
}

// Informational Log INFORMATIONAL level message.
func (g *GoLogger) Informational(f interface{}, v ...interface{}) {
	if LevelInfo > g.level {
		return
	}
	format := formatLog(f, v...)
	g.writeMsg(LevelInfo, format, v...)
}

// Debug Log DEBUG level message.
func (g *GoLogger) Debug(f interface{}, v ...interface{}) {
	if LevelDebug > g.level {
		return
	}
	format := formatLog(f, v...)
	g.writeMsg(LevelDebug, format, v...)
}

// Warn Log WARN level message.
// compatibility alias for Warning()
func (g *GoLogger) Warn(f interface{}, v ...interface{}) {
	if LevelWarn > g.level {
		return
	}
	format := formatLog(f, v...)
	g.writeMsg(LevelWarn, format, v...)
}

// Info Log INFO level message.
// compatibility alias for Informational()
func (g *GoLogger) Info(f interface{}, v ...interface{}) {
	if LevelInfo > g.level {
		return
	}
	format := formatLog(f, v...)
	g.writeMsg(LevelInfo, format, v...)
}

// Trace Log TRACE level message.
// compatibility alias for Debug()
func (g *GoLogger) Trace(f interface{}, v ...interface{}) {
	if LevelDebug > g.level {
		return
	}
	format := formatLog(f, v...)
	g.writeMsg(LevelDebug, format, v...)
}

// Flush flush all chan data.
func (g *GoLogger) Flush() {
	if g.asynchronous {
		g.signalChan <- "flush"
		g.wg.Wait()
		g.wg.Add(1)
		return
	}
	g.flush()
}

// Close close logger, flush all chan data and destroy all adapters in GoLogger.
func (g *GoLogger) Close() {
	if g.asynchronous {
		g.signalChan <- "close"
		g.wg.Wait()
		close(g.msgChan)
	} else {
		g.flush()
		for _, l := range g.outputs {
			l.Destroy()
		}
		g.outputs = nil
	}
	close(g.signalChan)
}

// Reset close all outputs, and set g.outputs to nil
func (g *GoLogger) Reset() {
	g.Flush()
	for _, l := range g.outputs {
		l.Destroy()
	}
	g.outputs = nil
}

func (g *GoLogger) flush() {
	if g.asynchronous {
		for {
			if len(g.msgChan) > 0 {
				bm := <-g.msgChan
				g.writeToLoggers(bm.when, bm.msg, bm.level)
				logMsgPool.Put(bm)
				continue
			}
			break
		}
	}
	for _, l := range g.outputs {
		l.Flush()
	}
}

// goLogger references the used application logger.
var goLogger *GoLogger = NewLogger()

// GetLogger returns the default GoLogger
func GetGoLogger() *GoLogger {
	return goLogger
}

var goLoggerMap = struct {
	sync.RWMutex
	logs map[string]*log.Logger
}{
	logs: map[string]*log.Logger{},
}

// GetLogger returns the default GoLogger
func GetLogger(prefixes ...string) *log.Logger {
	prefix := append(prefixes, "")[0]
	if prefix != "" {
		prefix = fmt.Sprintf(`[%s] `, strings.ToUpper(prefix))
	}
	goLoggerMap.RLock()
	l, ok := goLoggerMap.logs[prefix]
	if ok {
		goLoggerMap.RUnlock()
		return l
	}
	goLoggerMap.RUnlock()
	goLoggerMap.Lock()
	defer goLoggerMap.Unlock()
	l, ok = goLoggerMap.logs[prefix]
	if !ok {
		l = log.New(goLogger, prefix, 0)
		goLoggerMap.logs[prefix] = l
	}
	return l
}

// Reset will remove all the adapter
func Reset() {
	goLogger.Reset()
}

func Async(msgLen ...int64) *GoLogger {
	return goLogger.Async(msgLen...)
}

// SetLevel sets the global log level used by the simple logger.
func SetLevel(l int) {
	goLogger.SetLevel(l)
}

// EnableFuncCallDepth enable log funcCallDepth
func EnableFuncCallDepth(b bool) {
	goLogger.enableFuncCallDepth = b
}

// SetLogFuncCall set the CallDepth, default is 4
func SetLogFuncCall(b bool) {
	goLogger.EnableFuncCallDepth(b)
	goLogger.SetLogFuncCallDepth(4)
}

// SetLogFuncCallDepth set log funcCallDepth
func SetLogFuncCallDepth(d int) {
	goLogger.loggerFuncCallDepth = d
}

// SetLogger sets a new logger.
func SetLogger(adapter string, config ...string) error {
	err := goLogger.SetLogger(adapter, config...)
	if err != nil {
		return err
	}
	return nil
}

// Emergency logs a message at emergency level.
func Emergency(f interface{}, v ...interface{}) {
	goLogger.Emergency(f, v...)
}

// Alert logs a message at alert level.
func Alert(f interface{}, v ...interface{}) {
	goLogger.Alert(f, v...)
}

// Critical logs a message at critical level.
func Critical(f interface{}, v ...interface{}) {
	goLogger.Critical(f, v...)
}

// Error logs a message at error level.
func Error(f interface{}, v ...interface{}) {
	goLogger.Error(f, v...)
}

// Warning logs a message at warning level.
func Warning(f interface{}, v ...interface{}) {
	goLogger.Warn(f, v...)
}

// Warn compatibility alias for Warning()
func Warn(f interface{}, v ...interface{}) {
	goLogger.Warn(f, v...)
}

// Notice logs a message at notice level.
func Notice(f interface{}, v ...interface{}) {
	goLogger.Notice(f, v...)
}

// Informational logs a message at info level.
func Informational(f interface{}, v ...interface{}) {
	goLogger.Info(f, v...)
}

// Info compatibility alias for Informational()
func Info(f interface{}, v ...interface{}) {
	goLogger.Info(f, v...)
}

// Debug logs a message at debug level.
func Debug(f interface{}, v ...interface{}) {
	goLogger.Debug(f, v...)
}

// Trace logs a message at trace level.
// compatibility alias for Warning()
func Trace(f interface{}, v ...interface{}) {
	goLogger.Trace(f, v...)
}

func formatLog(f interface{}, v ...interface{}) string {
	var msg string
	switch f.(type) {
	case string:
		msg = f.(string)
		if len(v) == 0 {
			return msg
		}
		if strings.Contains(msg, "%") && !strings.Contains(msg, "%%") {
			// format string
		} else {
			// do not contain format char
			msg += strings.Repeat(" %v", len(v))
		}
	default:
		msg = fmt.Sprint(f)
		if len(v) == 0 {
			return msg
		}
		msg += strings.Repeat(" %v", len(v))
	}
	return msg
}