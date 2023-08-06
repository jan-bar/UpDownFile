package main

import (
	"compress/gzip"
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
	"text/template"
	"time"

	"github.com/pkg/errors"
)

func serverMain(exe string, args []string) error {
	var addrStr, basePath, certFile, keyFile, caFile string
	fs := flag.NewFlagSet(exe, flag.ExitOnError)
	fs.StringVar(&basePath, "p", ".", "path")
	fs.StringVar(&addrStr, "s", "", "ip:port")
	fs.StringVar(&certFile, "cert", "", "cert file")
	fs.StringVar(&keyFile, "key", "", "key file")
	fs.StringVar(&caFile, "ca", "ca.crt", "ca file")
	timeout := fs.Duration("t", time.Minute, "read header timeout")
	reg := fs.Bool("reg", false, "add right click registry")
	err := fs.Parse(args)
	if err != nil {
		return err
	}

	tcpAddr, err := net.ResolveTCPAddr("tcp", addrStr)
	if err != nil {
		return err
	}

	if *reg {
		if tcpAddr.Port < 1000 {
			return fmt.Errorf("usage: %s -s ip:port -reg\n", exe)
		}
		return createRegFile(exe, tcpAddr.String())
	}

	addr, err := net.ListenTCP("tcp", tcpAddr)
	if err != nil {
		return err
	}

	var urls []string
	addrStr = addr.Addr().String()
	if len(tcpAddr.IP) <= 0 {
		_, port, err := net.SplitHostPort(addrStr)
		if err != nil {
			return err
		}
		// 添加本机所有可用IP,组装Port
		if ips := InternalIp(); len(ips) > 0 {
			for _, v := range ips {
				urls = append(urls, v+":"+port)
			}
			addrStr = urls[0] // 取第一个IP作为默认url
		}
		urls = append(urls, "127.0.0.1:"+port) // 本地IP也可以
	} else {
		urls = []string{addrStr}
	}

	basePath, err = filepath.Abs(basePath)
	if err != nil {
		return err
	}

	//goland:noinspection HttpUrlsUsage
	tpl, err := template.New("").Parse(`{{range $i,$v := .urls}}
url: http://{{$v}}
{{- end}}

server:
    {{.exec}} -s {{.addr}} -p {{.dir}} -t {{.timeout}} -ca {{.ca}}{{if .cert}} -cert {{.cert}}{{end}}{{if .key}} -key {{.key}}{{end}}
registry:
    {{.exec}} -s {{.addr}} -reg
cli get:
    {{.exec}} cli -c{{if or .cert .key}} -ca {{.ca}}{{end}} "http://{{.addr}}/tmp.txt"
cli post:
    {{.exec}} cli -c{{if or .cert .key}} -ca {{.ca}}{{end}} -d @C:\tmp.txt "http://{{.addr}}/tmp.txt"

Get File:
    wget {{if or .cert .key}}--ca-certificate {{.ca}} {{end}}-c --content-disposition "http://{{.addr}}/tmp.txt"
    curl {{if or .cert .key}}--cacert {{.ca}} {{end}}-C - -OJ "http://{{.addr}}/tmp.txt"

Post File:
    wget {{if or .cert .key}}--ca-certificate {{.ca}} {{end}}-qO - --post-file=C:\tmp.txt "http://{{.addr}}/tmp.txt"
    wget {{if or .cert .key}}--ca-certificate {{.ca}} {{end}}-qO - --post-file=C:\tmp.txt "http://{{.addr}}/tmp.txt"
    curl {{if or .cert .key}}--cacert {{.ca}} {{end}}--data-binary @C:\tmp.txt "http://{{.addr}}/tmp.txt"
    curl {{if or .cert .key}}--cacert {{.ca}} {{end}}--data-binary @C:\tmp.txt "http://{{.addr}}/tmp.txt"
    curl {{if or .cert .key}}--cacert {{.ca}} {{end}}-F "file=@C:\tmp.txt" "http://{{.addr}}/"

Get Offset:
    curl {{if or .cert .key}}--cacert {{.ca}} {{end}}-H "Content-Type:application/offset" "http://{{.addr}}/tmp.txt"
    wget {{if or .cert .key}}--ca-certificate {{.ca}} {{end}}-qO - --header "Content-Type:application/offset" "http://{{.addr}}/tmp.txt"

Put File:
    curl {{if or .cert .key}}--cacert {{.ca}} {{end}}-C - -T C:\tmp.txt "http://{{.addr}}/tmp.txt"

`)
	if err != nil {
		return err
	}

	err = tpl.Execute(os.Stdout, map[string]any{
		"exec":    exe,
		"addr":    addrStr,
		"dir":     basePath,
		"timeout": timeout.String(),
		"urls":    urls,
		"cert":    certFile,
		"key":     keyFile,
		"ca":      caFile,
	})
	if err != nil {
		return err
	}

	srv := &http.Server{
		Addr:              addrStr,
		Handler:           &fileServer{path: basePath},
		ReadTimeout:       *timeout,
		ReadHeaderTimeout: *timeout,
	}

	if certFile != "" || keyFile != "" {
		if certFile == "" {
			return errors.New("cert file is null")
		}
		if keyFile == "" {
			return errors.New("key file is null")
		}
		return srv.ServeTLS(addr, certFile, keyFile)
	}
	return srv.Serve(addr)
}

type fileServer struct {
	path string
}

const (
	headerType   = "Content-Type"
	headerLength = "Content-Length"
	offsetLength = "Offset-Length"
	typeDefault  = "application/x-www-form-urlencoded" // curl,wget默认
	typeGzip     = "application/x-gzip"
	typeOffset   = "application/offset"
)

var (
	respOk = []byte("ok")
)

func (fs *fileServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var err error
	if r.RequestURI == "/favicon.ico" {
		_, err = w.Write(icoData) // 返回网页的图标
	} else {
		pool := bytePool.Get().(*poolByte)
		defer bytePool.Put(pool)

		switch r.Method {
		case http.MethodGet:
			err = fs.get(w, r, pool.buf)
		case http.MethodPost:
			err = fs.post(w, r, pool.buf)
		case http.MethodPut:
			err = fs.put(w, r, pool.buf)
		default:
			err = &webErr{msg: r.Method + " not support"}
		}
	}

	if err != nil {
		var e *webErr
		if !errors.As(err, &e) {
			e = &webErr{code: http.StatusInternalServerError, err: err}
		}
		// 先设置header,再写code,然后写消息体
		w.Header().Set(headerType, "text/plain;charset=utf-8")
		if e.code == 0 {
			e.code = http.StatusOK
		}
		w.WriteHeader(e.code)
		_, _ = fmt.Fprintf(w, "code: %d\n\nmsg: %s\n\n%+v", e.code, e.msg, e.err)
	}
}

type webErr struct {
	code int
	msg  string
	err  error
}

func (w *webErr) Error() string {
	return w.msg
}

// 浏览器获取目录时显示一个简易的web页面
var dirHtmlTpl = template.Must(template.New("").Parse(`<html lang="zh"><head><title>list dir</title></head><body>
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

<table border="1" align="center">
<tr><th>序号</th><th>类型</th><th>大小</th><th>修改时间</th><th>链接</th></tr>
{{- range $i,$v := .info}}
<tr><td>{{$v.Index}}</td><td>{{$v.Type}}</td><td>{{$v.Size}}</td><td>{{$v.Time}}</td><td><a href="{{$v.Href}}" download>{{$v.Name}}</a></td></tr>
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

func (fs *fileServer) respUrl(r *http.Request) string {
	u := &url.URL{Scheme: "http", Host: r.Host, Path: r.RequestURI}
	if r.TLS != nil {
		u.Scheme = "https"
	}
	return u.String()
}

//goland:noinspection GoUnhandledErrorResult
func (fs *fileServer) get(w http.ResponseWriter, r *http.Request, buf []byte) error {
	if r.Body != nil {
		defer r.Body.Close()
	}

	path := filepath.Join(fs.path, r.URL.Path)
	fi, err := os.Stat(path)
	if err != nil {
		return &webErr{
			code: http.StatusNotFound,
			msg:  path + " not found",
			err:  errors.WithStack(err),
		}
	}

	ht := r.Header.Get(headerType)
	if ht == typeOffset {
		if fi.IsDir() {
			return &webErr{msg: "unable to get directory size"}
		}
		// 获取服务器文件大小,用于断点上传文件,会返回curl断点上传命令
		size := string(strconv.AppendInt(buf[:0], fi.Size(), 10))
		w.Header().Set(offsetLength, size)
		// 组装curl断点上传的命令,返回给客户端直接执行
		_, err = fmt.Fprintf(w, "curl -C %s -T file %s\n", size, fs.respUrl(r))
		return err
	}

	if fi.IsDir() {
		dir, sortNum, err := fs.sortDir(path, r.FormValue("sort"))
		if err != nil {
			return &webErr{
				msg: "sort dir",
				err: errors.WithStack(err),
			}
		}

		type lineFileInfo struct {
			Index      int
			Type       string
			Size       string
			Time       string
			Href, Name string
		}

		info := make([]lineFileInfo, len(dir))
		for i, v := range dir {
			tmp := lineFileInfo{
				Index: i + 1,
				Size:  convertByte(buf[:0], v.Size()),
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

		err = dirHtmlTpl.Execute(w, map[string]any{
			"sort": sortNum,
			"info": info,
		})
		if err != nil {
			return &webErr{
				msg: "tpl.Execute",
				err: errors.WithStack(err),
			}
		}
	} else if ht == typeGzip {
		fr, err := os.Open(path)
		if err != nil {
			return &webErr{
				msg: "os.Open",
				err: errors.WithStack(err),
			}
		}

		gw, _ := gzip.NewWriterLevel(w, gzip.BestCompression)
		_, err = io.CopyBuffer(gw, fr, buf)
		gw.Close()
		fr.Close()
		if err != nil {
			return &webErr{
				msg: "io.CopyBuffer",
				err: errors.WithStack(err),
			}
		}
	} else {
		// todo 显示下载进度条
		http.ServeFile(w, r, path) // 支持断点下载
	}
	return nil
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

func (fs *fileServer) sortDir(dir string, s string) (list []os.FileInfo, st int, err error) {
	st, _ = strconv.Atoi(s)
	if st < sortDirTypeByNameAsc || st > sortDirTypeByExtDesc {
		st = sortDirTypeByNameAsc
	}

	var fr *os.File
	fr, err = os.Open(dir)
	if err != nil {
		return
	}

	list, err = fr.Readdir(-1)
	_ = fr.Close()
	if err != nil {
		return
	}
	sort.Sort(&dirSort{fi: list, st: st})
	return
}

//goland:noinspection GoUnhandledErrorResult
func (fs *fileServer) post(w http.ResponseWriter, r *http.Request, buf []byte) error {
	if r.Body == nil {
		return &webErr{msg: "body is null"}
	}

	var (
		path string
		fr   io.ReadCloser
		err  error

		size, cur int64
		fileFlag  = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	)

	switch r.Header.Get(headerType) {
	case typeDefault: // curl,wget,cli 这三种方式上传
		size, err = parseInt64(r.Header.Get(headerLength))
		if err != nil {
			return err
		}

		cur, err = parseInt64(r.Header.Get(offsetLength))
		if err == nil {
			fileFlag = os.O_CREATE | os.O_APPEND
		}

		// 普通二进制上传文件,消息体直接是文件内容
		fr, path = r.Body, filepath.Join(fs.path, r.URL.Path)
	case typeGzip: // 上传gzip文件,服务器自动解压
		fr, err = gzip.NewReader(r.Body)
		defer r.Body.Close()
		if err != nil {
			return err
		}
		path = filepath.Join(fs.path, r.URL.Path)
	default:
		rf, rh, err := r.FormFile("file")
		if err != nil {
			return err
		}
		// 使用浏览器上传 或 curl -F "file=@C:\tmp.txt",这两种方式
		fr, size, path = rf, rh.Size, filepath.Join(fs.path, r.URL.Path, rh.Filename)
	}
	defer fr.Close()

	fw, err := os.OpenFile(path, fileFlag, fileMode)
	if err != nil {
		return err
	}
	fmt.Println(size, cur)

	_, err = io.CopyBuffer(fw, fr, buf)
	_ = fw.Close()
	if err != nil {
		return err
	}
	_, err = w.Write(respOk)
	return err
}

//goland:noinspection GoUnhandledErrorResult,HttpUrlsUsage
func (fs *fileServer) put(w http.ResponseWriter, r *http.Request, buf []byte) error {
	if r.Body == nil {
		return &webErr{msg: "body is null"}
	}
	defer r.Body.Close()

	var (
		fw        *os.File
		cur, size int64
		path      = filepath.Join(fs.path, r.URL.Path)
	)

	fi, err := os.Stat(path)
	if err == nil {
		// 文件存在,检查客户端断点续传Header
		if cur, _, size, err = scanRangeSize(r.Header); err == nil {
			fw, err = os.OpenFile(path, os.O_RDWR|os.O_CREATE, fileMode)
			if err != nil {
				return err
			}
			defer fw.Close()

			nSize := fi.Size()
			if nSize == size {
				return &webErr{msg: "file upload is complete"}
			}

			// 需要返回客户端断点上传的命令,指定文件偏移
			if (cur == 0 && nSize > 0) || cur > nSize {
				if resp := fs.respUrl(r); nSize == 0 {
					_, err = fmt.Fprintf(w, "curl -T file %s\n", resp)
				} else {
					_, err = fmt.Fprintf(w, "curl -C %d -T file %s\n", nSize, resp)
				}
				return err
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
		fw, err = os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_TRUNC, fileMode)
		if err != nil {
			return err
		}
		defer fw.Close()

		size, err = parseInt64(r.Header.Get(headerLength))
		if err != nil {
			return err
		}
		cur = 0
	}

	_, err = io.CopyBuffer(fw, r.Body, buf)
	if err != nil {
		return err
	}
	_, err = w.Write(respOk)
	return err
}
