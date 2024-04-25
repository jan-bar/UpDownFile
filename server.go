package main

import (
	"bytes"
	"compress/gzip"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"text/template"
	"time"

	"github.com/vbauerster/mpb/v8"
	"github.com/vbauerster/mpb/v8/decor"
	"gopkg.in/natefinch/lumberjack.v2"
	"gopkg.in/yaml.v3"
)

func serverMain(exe string, args []string) error {
	fs := flag.NewFlagSet(exe, flag.ExitOnError)
	cnf := fs.String("c", "server.yaml", "config file")
	path := fs.String("p", ".", "path")
	listen := fs.String("s", "", "ip:port")
	reg := fs.Bool("reg", false, "add right click registry")
	err := fs.Parse(args)
	if err != nil {
		return err
	}

	var config struct {
		Listen      string `yaml:"listen"`
		Path        string `yaml:"path"`
		Auth        string `yaml:"auth"`
		Timeout     string `yaml:"timeout"`
		Deny        bool   `yaml:"deny"`
		Certificate struct {
			Domain string `yaml:"domain"`
			Cert   string `yaml:"cert"`
			Key    string `yaml:"key"`
			Ca     string `yaml:"ca"`
		} `yaml:"certificate"`
		Log struct {
			Logger   *lumberjack.Logger `yaml:"logger"`
			Template string             `yaml:"template"`
		} `yaml:"log"`
	}

	data, err := os.ReadFile(*cnf)
	if err != nil {
		return err
	}

	err = yaml.Unmarshal(data, &config)
	if err != nil {
		return err
	}

	// 常用参数覆盖配置文件
	if *path != "" {
		config.Path = *path
	}
	if *listen != "" {
		config.Listen = *listen
	}

	timeout := time.Minute
	if t, err := time.ParseDuration(config.Timeout); err == nil {
		timeout = t // 配置文件读取超时时间成功
	}

	tcpAddr, err := net.ResolveTCPAddr("tcp", config.Listen)
	if err != nil {
		return err
	}

	if *reg {
		if tcpAddr.Port < 80 {
			return fmt.Errorf("usage: %s -s ip:port -reg\n", exe)
		}
		return createRegFile(exe, tcpAddr.String())
	}

	uri := &url.URL{Scheme: schemeHttp}
	if config.Certificate.Cert != "" || config.Certificate.Key != "" {
		if config.Certificate.Cert == "" {
			return errors.New("cert file is null")
		}
		if config.Certificate.Key == "" {
			return errors.New("key file is null")
		}
		uri.Scheme = schemeHttps
	}

	addr, err := net.ListenTCP("tcp", tcpAddr)
	if err != nil {
		return err
	}

	var urls []string
	if len(tcpAddr.IP) == 0 {
		if ips := InternalIp(); len(ips) > 0 {
			for _, v := range ips {
				tcpAddr.IP = v // 拼接本机所有ip:port
				uri.Host = tcpAddr.String()
				urls = append(urls, uri.String())
			}
		}

		tcpAddr.IP = net.IPv4(127, 0, 0, 1)
	}
	uri.Host = tcpAddr.String()
	urls = append(urls, uri.String())

	if config.Certificate.Domain == "" {
		uri.Host = tcpAddr.IP.String()
	} else {
		uri.Host = config.Certificate.Domain
	}

	if !((uri.Scheme == schemeHttp && tcpAddr.Port == 80) ||
		(uri.Scheme == schemeHttps && tcpAddr.Port == 443)) {
		// 不是特殊协议和端口,需要拼接端口,特殊协议不需要带上端口
		uri.Host = net.JoinHostPort(uri.Host, strconv.Itoa(tcpAddr.Port))
	}
	addrStr := uri.String()

	config.Path, err = filepath.Abs(config.Path)
	if err != nil {
		return err
	}

	tpl, err := template.New("").Parse(`{{range $i,$v := .urls}}
web service: {{$v}}
{{- end}}

server:
    {{.exec}} -s {{.listen}} -p {{.dir}} -t {{.timeout}}{{if .auth}} -auth "{{.auth}}"{{end}}{{if .cert}} -ca {{.ca}} -cert {{.cert}}{{end}}{{if .key}} -key {{.key}}{{end}}{{if .domain}} -d {{.domain}}{{end}}
registry:
    {{.exec}} -s {{.listen}} -reg
cli get:
    {{.exec}} cli -c{{if .auth}} -auth "{{.auth}}"{{end}}{{if or .cert .key}} -ca {{.ca}}{{end}} -o C:\{{.example}} "{{.addr}}/{{.example}}"
cli post:
    {{.exec}} cli -c{{if .auth}} -auth "{{.auth}}"{{end}}{{if or .cert .key}} -ca {{.ca}}{{end}} -d @C:\{{.example}} "{{.addr}}/{{.example}}"

Get File:
    wget {{if or .cert .key}}--ca-certificate {{.ca}} {{end}}{{if .wget}}{{.wget}} {{end}}-c --content-disposition "{{.addr}}/{{.example}}"
    curl {{if or .cert .key}}--cacert {{.ca}} {{end}}{{if .auth}}-u "{{.auth}}" {{end}}-C - -OJ "{{.addr}}/{{.example}}"

Post File:
    wget {{if or .cert .key}}--ca-certificate {{.ca}} {{end}}{{if .wget}}{{.wget}} {{end}}-qO - --post-file=C:\{{.example}} "{{.addr}}/{{.example}}"
    curl {{if or .cert .key}}--cacert {{.ca}} {{end}}{{if .auth}}-u "{{.auth}}" {{end}}--data-binary @C:\{{.example}} "{{.addr}}/{{.example}}"
    curl {{if or .cert .key}}--cacert {{.ca}} {{end}}{{if .auth}}-u "{{.auth}}" {{end}}-F "file=@C:\{{.example}}" "{{.addr}}/{{.example}}/"

Get Offset:
    curl {{if or .cert .key}}--cacert {{.ca}} {{end}}{{if .auth}}-u "{{.auth}}" {{end}}-H "Content-Type:application/offset" "{{.addr}}/{{.example}}"
    wget {{if or .cert .key}}--ca-certificate {{.ca}} {{end}}{{if .wget}}{{.wget}} {{end}}-qO - --header "Content-Type:application/offset" "{{.addr}}/{{.example}}"

Put File:
    curl {{if or .cert .key}}--cacert {{.ca}} {{end}}{{if .auth}}-u "{{.auth}}" {{end}}-C - -T C:\{{.example}} "{{.addr}}/{{.example}}"

`)
	if err != nil {
		return err
	}

	var wget string
	if user, pass, ok := strings.Cut(config.Auth, ":"); ok {
		wget = fmt.Sprintf(`--user "%s" --password "%s"`, user, pass)
	}

	err = tpl.Execute(os.Stdout, map[string]any{
		"exec":    exe,
		"listen":  addr.Addr().String(),
		"urls":    urls,
		"addr":    addrStr,
		"domain":  config.Certificate.Domain,
		"example": "example.txt",
		"dir":     config.Path,
		"timeout": timeout.String(),
		"cert":    config.Certificate.Cert,
		"key":     config.Certificate.Key,
		"ca":      config.Certificate.Ca,
		"auth":    config.Auth,
		"wget":    wget,
	})
	if err != nil {
		return err
	}

	srh := &fileServer{
		path:   config.Path,
		pBar:   newMpbProgress(),
		scheme: uri.Scheme,
		auth:   config.Auth,
		deny:   config.Deny,
		out:    os.Stdout,
	}

	if config.Log.Logger != nil && config.Log.Logger.Filename != "-" {
		//goland:noinspection GoUnhandledErrorResult
		defer config.Log.Logger.Close()
		// log.logger.filename = "-" 或 不配置log.logger 时日志输出到 stdout
		// 否则按照日志库的配置输出日志,支持日志文件按大小和天数切分
		srh.out = config.Log.Logger
	}

	if config.Log.Template != "" {
		// 当配置日志模板时,使用自定义模板输出日志
		srh.log, err = template.New("").Funcs(template.FuncMap{
			"time": func(layout string) any {
				switch now := time.Now(); layout {
				case "Unix":
					return now.Unix()
				case "UnixMilli":
					return now.UnixMilli()
				case "UnixMicro":
					return now.UnixMicro()
				case "UnixNano":
					return now.UnixNano()
				default:
					return now.Format(layout)
				}
			},
		}).Parse(config.Log.Template)
		if err != nil {
			return err
		}
	}

	srv := &http.Server{
		Addr:    addrStr,
		Handler: srh,

		ReadTimeout:       timeout,
		ReadHeaderTimeout: timeout,
	}

	if uri.Scheme == schemeHttps {
		return srv.ServeTLS(addr, config.Certificate.Cert, config.Certificate.Key)
	}
	return srv.Serve(addr)
}

type fileServer struct {
	path   string
	pBar   *mpb.Progress
	scheme string
	auth   string
	deny   bool
	out    io.Writer
	log    *template.Template
}

const (
	headerType   = "Content-Type"
	headerLength = "Content-Length"
	headerRange  = "Range"
	offsetLength = "Offset-Length"
	offsetAppend = "append"
	typeDefault  = "application/x-www-form-urlencoded" // curl,wget默认
	typeGzip     = "application/x-gzip"
	typeOffset   = "application/offset"

	schemeHttp  = "http"
	schemeHttps = "https"
)

var (
	respOk = []byte("ok")
)

func (fs *fileServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Body != nil {
		//goland:noinspection GoUnhandledErrorResult
		defer r.Body.Close()
	}

	if r.RequestURI == "/favicon.ico" {
		//goland:noinspection GoUnhandledErrorResult
		w.Write(icoData) // 返回网页的图标
		return
	}

	var err error
	if fs.auth != "" {
		if user, pass, ok := r.BasicAuth(); !ok || user+":"+pass != fs.auth {
			w.Header().Add("WWW-Authenticate", `Basic realm="Please Authenticate"`)
			err = &webErr{code: http.StatusUnauthorized, msg: "Authenticate Error"}
		}
	}

	if err == nil {
		pool := bytePool.Get().(*poolByte)
		defer bytePool.Put(pool)

		if fs.log != nil {
			buf := bytes.NewBuffer(pool.buf[:0])
			err = fs.log.Execute(buf, map[string]any{
				"req": r,
			})
			//goland:noinspection GoUnhandledErrorResult
			if err != nil {
				fmt.Fprintf(fs.out, "log error: %v\n", err)
			} else {
				fmt.Fprintf(fs.out, "%s", buf.Bytes())
			}
		}

		switch r.Method {
		case http.MethodGet:
			err = fs.get(w, r, pool.buf)
		case http.MethodPost:
			err = fs.post(w, r, pool.buf)
		case http.MethodHead:
			err = fs.head(w, r)
		case http.MethodPut:
			err = fs.put(w, r, pool.buf)
		default:
			err = &webErr{
				code: http.StatusMethodNotAllowed,
				msg:  r.Method + " not support",
			}
		}
	}

	if err != nil {
		var e *webErr
		if !errors.As(err, &e) {
			e = &webErr{msg: "Internal Server Error", err: err}
		}

		if e.code == 0 {
			e.code = http.StatusInternalServerError
		}

		w.Header().Set(headerType, "text/html;charset=utf-8")
		w.WriteHeader(e.code)
		_, _ = fmt.Fprintf(w, `<html><head><title>UpDownFile</title></head><body><center><h1>%s</h1></center><hr><center>%v</center></body></html>`, e.msg, e.err)
	}
}

type webErr struct {
	err  error
	msg  string
	code int
}

func (w *webErr) Error() string {
	return w.msg
}

func (fs *fileServer) open(r *http.Request) (fr *os.File, fi os.FileInfo, err error) {
	fr, err = os.Open(filepath.Join(fs.path, r.URL.Path))
	if err != nil {
		if os.IsNotExist(err) {
			err = &webErr{
				code: http.StatusNotFound,
				msg:  "404 Not Found",
				err:  err,
			}
		}
		return
	}

	fi, err = fr.Stat()
	if err != nil {
		_ = fr.Close()
	} else if fs.deny && fi.IsDir() {
		err = &webErr{
			code: http.StatusForbidden,
			msg:  "deny directory request",
		}
	}
	return
}

func (fs *fileServer) head(w http.ResponseWriter, r *http.Request) error {
	fr, fi, err := fs.open(r)
	if err != nil {
		return err
	}
	http.ServeContent(w, r, fr.Name(), fi.ModTime(), fr)
	_ = fr.Close()
	return nil
}

var dirHtmlTpl = sync.OnceValue(func() *template.Template {
	// 浏览器获取目录时显示一个简易的web页面,有需要时只加载1次
	return template.Must(template.New("").Parse(`<html lang="zh"><head><title>list dir</title></head><body>
<div style="position:fixed;bottom:20px;right:10px"><p>
<label><input type="radio" name="sort" onclick="sortDir(0)"{{if eq .sort 0}}checked{{end}}>名称升序</label>
<label><input type="radio" name="sort" onclick="sortDir(1)"{{if eq .sort 1}}checked{{end}}>名称降序</label>
</p><p>
<label><input type="radio" name="sort" onclick="sortDir(2)"{{if eq .sort 2}}checked{{end}}>时间升序</label>
<label><input type="radio" name="sort" onclick="sortDir(3)"{{if eq .sort 3}}checked{{end}}>时间降序</label>
</p><p>
<label><input type="radio" name="sort" onclick="sortDir(4)"{{if eq .sort 4}}checked{{end}}>大小升序</label>
<label><input type="radio" name="sort" onclick="sortDir(5)"{{if eq .sort 5}}checked{{end}}>大小降序</label>
</p><p>
<label><input type="radio" name="sort" onclick="sortDir(6)"{{if eq .sort 6}}checked{{end}}>后缀升序</label>
<label><input type="radio" name="sort" onclick="sortDir(7)"{{if eq .sort 7}}checked{{end}}>后缀降序</label>
</p>
<p><input type="file" id="file"></p>
<progress value="0" id="progress"></progress>
<p><input type="button" onclick="uploadFile()" value="上传文件"></p>
<input type="button" onclick="backSuper()" value="返回上级"/>
<a href="#top" style="margin:5px">顶部</a>
<a href="#bottom">底部</a>
</div>
<style>td:nth-child(3){text-align:right}</style>
<table border="1" align="center">
<tr><th>序号</th><th>类型</th><th>大小</th><th>修改时间</th><th>链接</th></tr>
{{- range $i,$v := .info}}
<tr><td>{{$v.Index}}</td><td>{{$v.Type}}</td><td>{{$v.Size}}</td><td>{{$v.Time}}</td><td><a href="{{$v.Href}}"{{if eq $v.Type "F"}}download{{end}}>{{$v.Name}}</a></td></tr>
{{- end}}
</table>

<a name="bottom"></a>
<script>
function uploadFile() {
	let upload = document.getElementById('file').files[0]
	if (!upload) {
		alert('请选择上传文件')
		return
	}
	let params = new FormData()
	params.append('file', upload)
	let xhr = new XMLHttpRequest()
	xhr.onerror = function() {
		alert('请求失败')
	}
	xhr.onreadystatechange = function() {
		if (xhr.readyState === 4) {
			if (xhr.status === 200) {
				if (xhr.responseText === "ok") {
					window.location.reload()
				} else {
					alert(xhr.responseText)
				}
			} else {
				alert(xhr.status)
			}
		}
	}
	let progress = document.getElementById('progress')
	xhr.upload.onprogress = function(e) {
		progress.value = e.loaded
		progress.max = e.total
	}
	xhr.open('POST', window.location.pathname, true)
	xhr.send(params)
}
function sortDir(type) {
	window.location.href = window.location.origin + window.location.pathname + '?sort=' + type
}
function backSuper() {
	let url = window.location.pathname
	let i = url.length - 1
	for (;i >= 0 && url[i] === '/';i--){}
	for (;i >= 0 && url[i] !== '/';i--){}
	window.location.href = window.location.origin + url.substring(0,i+1)
}</script></body></html>`))
})

func (fs *fileServer) get(w http.ResponseWriter, r *http.Request, buf []byte) error {
	fr, fi, err := fs.open(r)
	if err != nil {
		return err
	}
	//goland:noinspection GoUnhandledErrorResult
	defer fr.Close()

	size := fi.Size()
	w.Header().Set(offsetLength, string(strconv.AppendInt(buf[:0], size, 10)))

	ht := r.Header.Get(headerType)
	if ht == typeOffset {
		if fi.IsDir() {
			return &webErr{
				code: http.StatusForbidden,
				msg:  "unable to get directory size",
			}
		}
		return fs.offset(w, r, size)
	}

	switch {
	case fi.IsDir():
		dir, sortNum, err := fs.sortDir(fr, r.FormValue("sort"))
		if err != nil {
			return err
		}

		type lineFileInfo struct {
			Type  string
			Size  string
			Time  string
			Href  string
			Name  string
			Index int
		}

		info := make([]lineFileInfo, len(dir))
		for i, v := range dir {
			tmp := lineFileInfo{
				Index: i + 1,
				Size:  convertByte(v.Size(), false),
				Time:  string(v.ModTime().AppendFormat(buf[:0], time.DateTime)),
				Name:  v.Name(),
			}

			href := append(buf[:0], url.PathEscape(v.Name())...)
			if v.IsDir() {
				tmp.Type = "D"
				href = append(href, '/')
			} else {
				tmp.Type = "F"
			}
			tmp.Href = string(href)
			info[i] = tmp
		}

		err = dirHtmlTpl().Execute(w, map[string]any{
			"sort": sortNum,
			"info": info,
		})
		if err != nil {
			return err
		}
	case ht == typeGzip:
		gw, _ := gzip.NewWriterLevel(w, gzip.BestCompression)
		pw := &progressBar{w: gw, b: newMpbBar(fs.pBar, http.MethodGet, fr.Name(), size)}
		_, err = io.CopyBuffer(pw, fr, buf)
		pw.Close()
		_ = gw.Close()
		if err != nil {
			return err
		}
	default:
		pb := newMpbBar(fs.pBar, http.MethodGet, fr.Name(), 0)
		pw := &progressBar{ResponseWriter: w, w: w, b: pb, fn: func() {
			t, e := parseInt64(w.Header().Get(headerLength))
			pb.SetTotal(t, e != nil) // 延迟设置进度条,适应分片下载逻辑
		}}
		http.ServeContent(pw, r, fr.Name(), fi.ModTime(), fr)
		pw.Close()
	}
	return nil
}

func newMpbProgress() *mpb.Progress {
	return mpb.New(
		mpb.WithWidth(30),
		mpb.PopCompletedMode(), // 进度条完成后不再渲染
		mpb.WithAutoRefresh(),
	)
}
func newMpbBar(bar *mpb.Progress, mode, path string, size int64) *mpb.Bar {
	return bar.AddBar(size, mpb.AppendDecorators(
		decor.Any(func(st decor.Statistics) string {
			var p int64
			switch {
			case st.Total <= 0:
			case st.Current >= st.Total:
				p = 100
			default:
				p = 100 * st.Current / st.Total
			}

			cur := convertByte(st.Current, true)
			if st.Completed {
				return fmt.Sprintf("done %s %s %s", cur, mode, path)
			}
			return fmt.Sprintf("%3d%% %s %s %s", p, cur, mode, path)
		}, decor.WCSyncWidth),
	))
}

type progressBar struct {
	http.ResponseWriter
	fn func()

	w io.Writer
	r io.Reader
	b *mpb.Bar
}

func (pb *progressBar) Write(p []byte) (n int, err error) {
	if pb.fn != nil {
		pb.fn() // execute only once
		pb.fn = nil
	}

	n, err = pb.w.Write(p)
	pb.b.IncrBy(n)
	return
}
func (pb *progressBar) Read(p []byte) (n int, err error) {
	n, err = pb.r.Read(p)
	pb.b.IncrBy(n)
	return
}
func (pb *progressBar) Close() {
	pb.b.Abort(false)
	pb.b.EnableTriggerComplete()
}

const (
	sortDirTypeByNameAsc = iota
	sortDirTypeByNameDesc
	sortDirTypeByTimeAsc
	sortDirTypeByTimeDesc
	sortDirTypeBySizeAsc
	sortDirTypeBySizeDesc
	sortDirTypeByExtAsc
	sortDirTypeByExtDesc
)

type dirSort struct {
	fi []os.FileInfo
	st int
}

func (d *dirSort) Len() int {
	return len(d.fi)
}

func (d *dirSort) defaultSort(x, y int) bool {
	lx, ly := len(d.fi[x].Name()), len(d.fi[y].Name())
	if lx == ly {
		return d.fi[x].Name() < d.fi[y].Name()
	}
	return lx < ly
}

func (d *dirSort) Less(x, y int) bool {
	if d.fi[x].IsDir() != d.fi[y].IsDir() {
		return d.fi[x].IsDir() // 文件夹永远排在文件前面
	}
	switch d.st {
	default:
		fallthrough // 默认使用文件名升序
	case sortDirTypeByNameAsc:
		return d.defaultSort(x, y)
	case sortDirTypeByNameDesc:
		lx, ly := len(d.fi[x].Name()), len(d.fi[y].Name())
		if lx == ly {
			return d.fi[x].Name() > d.fi[y].Name()
		}
		return lx > ly
	case sortDirTypeByTimeAsc:
		tx, ty := d.fi[x].ModTime(), d.fi[y].ModTime()
		if tx.Unix() == ty.Unix() {
			return d.defaultSort(x, y)
		}
		return tx.Before(ty)
	case sortDirTypeByTimeDesc:
		tx, ty := d.fi[x].ModTime(), d.fi[y].ModTime()
		if tx.Unix() == ty.Unix() {
			return d.defaultSort(x, y)
		}
		return tx.After(ty)
	case sortDirTypeBySizeAsc:
		sx, sy := d.fi[x].Size(), d.fi[y].Size()
		if sx == sy {
			return d.defaultSort(x, y)
		}
		return sx < sy
	case sortDirTypeBySizeDesc:
		sx, sy := d.fi[x].Size(), d.fi[y].Size()
		if sx == sy {
			return d.defaultSort(x, y)
		}
		return sx > sy
	case sortDirTypeByExtAsc:
		if !d.fi[x].IsDir() && !d.fi[y].IsDir() {
			return filepath.Ext(d.fi[x].Name()) < filepath.Ext(d.fi[y].Name())
		}
		return d.defaultSort(x, y)
	case sortDirTypeByExtDesc:
		if !d.fi[x].IsDir() && !d.fi[y].IsDir() {
			return filepath.Ext(d.fi[x].Name()) > filepath.Ext(d.fi[y].Name())
		}
		return d.defaultSort(x, y)
	}
}

func (d *dirSort) Swap(x, y int) {
	d.fi[x], d.fi[y] = d.fi[y], d.fi[x]
}

func (fs *fileServer) sortDir(dir *os.File, s string) (list []os.FileInfo, st int, err error) {
	st, _ = strconv.Atoi(s)
	if st < sortDirTypeByNameAsc || st > sortDirTypeByExtDesc {
		st = sortDirTypeByNameAsc
	}
	list, err = dir.Readdir(-1)
	if err != nil {
		return
	}
	sort.Sort(&dirSort{fi: list, st: st})
	return
}

func (fs *fileServer) post(w io.Writer, r *http.Request, buf []byte) error {
	if r.Body == nil {
		return &webErr{
			code: http.StatusBadRequest,
			msg:  "body is null",
		}
	}

	var (
		path = filepath.Join(fs.path, r.URL.Path)
		fr   io.ReadCloser
		err  error
		size int64
		fg   = flagW
	)

	switch ht := r.Header.Get(headerType); ht {
	case typeDefault: // curl,wget,cli 这三种方式上传
		size, err = parseInt64(r.Header.Get(headerLength))
		if err != nil {
			return err
		}

		if r.Header.Get(offsetLength) == offsetAppend {
			fg = flagA // 客户端告诉服务器断点上传
		}

		fr = r.Body
	case typeGzip:
		fr, err = gzip.NewReader(r.Body)
		if err != nil {
			return err
		}
		// 服务器解析gzip数据,直接使用自定义长度
		size, err = parseInt64(r.Header.Get(offsetLength))
		if err != nil {
			return err
		}
	default:
		if !strings.HasPrefix(ht, "multipart/form-data;") {
			return &webErr{
				code: http.StatusForbidden,
				msg:  fmt.Sprintf("%s:%s not support", headerType, ht),
			}
		}

		rf, rh, err := r.FormFile("file")
		if err != nil {
			return err
		}
		// 浏览器上传 或 curl -F "file=@xx"
		// r.FormFile 会先把文件下载好,下面只是复制,因此进度条已客户端为准
		fr, size = rf, rh.Size
		path = filepath.Join(path, rh.Filename)
	}
	//goland:noinspection GoUnhandledErrorResult
	defer fr.Close()

	fw, err := os.OpenFile(path, fg, fileMode)
	if err != nil {
		return err
	}

	pw := &progressBar{w: fw, b: newMpbBar(fs.pBar, http.MethodPost, path, size)}
	_, err = io.CopyBuffer(pw, fr, buf)
	pw.Close()
	_ = fw.Close()
	if err != nil {
		return err
	}
	_, err = w.Write(respOk)
	return err
}

func (fs *fileServer) put(w io.Writer, r *http.Request, buf []byte) error {
	if r.Body == nil {
		return &webErr{
			code: http.StatusBadRequest,
			msg:  "body is null",
		}
	}

	var (
		fw   *os.File
		cur  int64
		size int64
		path = filepath.Join(fs.path, r.URL.Path)
	)

	fi, err := os.Stat(path)
	if err == nil {
		// 文件存在,检查客户端断点续传Header
		if cur, _, size, err = scanRangeSize(r.Header); err == nil {
			fw, err = os.OpenFile(path, os.O_RDWR|os.O_CREATE, fileMode)
			if err != nil {
				return err
			}
			//goland:noinspection GoUnhandledErrorResult
			defer fw.Close()

			nSize := fi.Size()
			if nSize >= size {
				return &webErr{
					code: http.StatusConflict,
					msg:  "file upload is complete",
				}
			}

			// 需要返回客户端断点上传的命令,指定文件偏移
			if (cur == 0 && nSize > 0) || cur > nSize {
				return fs.offset(w, r, nSize)
			}

			if cur > 0 { // 从指定位置继续写文件
				_, err = fw.Seek(cur, io.SeekStart)
				if err != nil {
					return err
				}
			}
		}
	}

	if fw == nil {
		fw, err = os.OpenFile(path, flagW, fileMode)
		if err != nil {
			return err
		}
		//goland:noinspection GoUnhandledErrorResult
		defer fw.Close()
	}

	size, err = parseInt64(r.Header.Get(headerLength))
	if err != nil {
		return err
	}

	pw := &progressBar{w: fw, b: newMpbBar(fs.pBar, http.MethodPut, path, size)}
	_, err = io.CopyBuffer(pw, r.Body, buf)
	pw.Close()
	if err != nil {
		return err
	}
	_, err = w.Write(respOk)
	return err
}

func (fs *fileServer) offset(w io.Writer, r *http.Request, size int64) (err error) {
	var ( // 返回客户端断点上传的命令行参数
		uri  = &url.URL{Scheme: fs.scheme, Host: r.Host, Path: r.RequestURI}
		name = filepath.Base(uri.Path)
	)
	if size == 0 {
		_, err = fmt.Fprintf(w, "curl -T %s %s\n", name, uri)
	} else {
		_, err = fmt.Fprintf(w, "curl -C %d -T %s %s\n", size, name, uri)
	}
	return
}
