package main

import (
	"crypto/cipher"
	"crypto/md5"
	_ "embed"
	"flag"
	"fmt"
	"hash"
	"html/template"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unsafe"
)

type poolByte struct{ buf []byte } // 这种方式才能过语法检查

var (
	bytePool = sync.Pool{New: func() interface{} {
		return &poolByte{buf: make([]byte, 32<<10)}
	}}

	//go:embed fileServer.ico
	icoData []byte // 嵌入图标文件

	basePath   string // 传入路径的绝对路径
	useEncrypt string // 加密秘钥
	execPath   string // 可执行程序绝对路径
)

//goland:noinspection HttpUrlsUsage
func main() {
	var err error // 获取程序运行路径
	execPath, err = os.Executable()
	if err != nil {
		panic(err)
	}

	if len(os.Args) > 2 && os.Args[1] == "cli" {
		if err = clientMain(); err != nil {
			fmt.Println(err.Error())
		}
		return
	}

	flag.StringVar(&basePath, "p", ".", "path")
	var addrStr string
	flag.StringVar(&addrStr, "s", "", "ip:port")
	flag.StringVar(&useEncrypt, "e", "", "encrypt data")
	timeout := flag.Duration("t", time.Second*30, "server timeout")
	reg := flag.Bool("reg", false, "add right click registry")
	flag.Parse()

	tcpAddr, err := net.ResolveTCPAddr("tcp", addrStr)
	if err != nil {
		panic(err)
	}

	if *reg {
		if len(tcpAddr.IP) <= 0 || tcpAddr.Port < 1000 {
			fmt.Printf("usage: %s -s ip:port -reg\n", execPath)
		} else {
			if err = createRegFile(tcpAddr.String()); err != nil {
				panic(err)
			}
		}
		return
	}

	addr, err := net.ListenTCP("tcp", tcpAddr)
	if err != nil {
		panic(err)
	}

	var urls []string
	addrStr = addr.Addr().String()
	if len(tcpAddr.IP) <= 0 {
		_, port, err := net.SplitHostPort(addrStr)
		if err != nil {
			panic(err)
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
		panic(err)
	}

	tpl, err := template.New("").Parse(`{{range $i,$v := .urls}}
url: http://{{$v}}
{{- end}}

server:
    {{.exec}} -s {{.addr}} -p {{.dir}} -t {{.timeout}}{{if .pass}} -e {{.pass}}{{end}}
cli get:
    {{.exec}} cli -c{{if .pass}} -e {{.pass}}{{end}} http://{{.addr}}/tmp.txt
cli post:
    {{.exec}} cli -c{{if .pass}} -e {{.pass}}{{end}} -d @C:\tmp.txt http://{{.addr}}/tmp.txt

Get File:
    wget -c --content-disposition http://{{.addr}}/tmp.txt
    curl -C - -OJ http://{{.addr}}/tmp.txt

Post File:
    wget -qO - --post-file=C:\tmp.txt http://{{.addr}}/tmp.txt
    curl --data-binary @C:\tmp.txt http://{{.addr}}/tmp.txt
    curl -F "file=@C:\tmp.txt" http://{{.addr}}/

Upload Size
    curl -H "Content-Type:application/janbar" http://{{.addr}}/tmp.txt
    wget -qO - --header "Content-Type:application/janbar" http://{{.addr}}/tmp.txt
`)
	if err != nil {
		panic(err)
	}
	// 渲染命令行帮助
	err = tpl.Execute(os.Stdout, map[string]interface{}{
		"exec":    execPath,
		"addr":    addrStr,
		"dir":     basePath,
		"timeout": timeout.String(),
		"pass":    useEncrypt,
		"urls":    urls,
	})
	if err != nil {
		panic(err)
	}

	http.HandleFunc("/", upDownFile)
	http.HandleFunc("/favicon.ico", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(icoData) // 网页的图标
	})
	err = (&http.Server{ReadHeaderTimeout: *timeout}).Serve(addr)
	if err != nil {
		panic(err)
	}
}

func createRegFile(addr string) error {
	if runtime.GOOS != "windows" {
		return nil // 仅window下才生成右键快捷键
	}

	fw, err := os.Create("addRightClickRegistry.reg")
	if err != nil {
		return err
	}
	//goland:noinspection GoUnhandledErrorResult
	defer fw.Close()

	icoFile := filepath.Join(filepath.Dir(execPath), "fileServer.ico")
	err = os.WriteFile(icoFile, icoData, fileMode)
	if err != nil {
		return err
	}

	_, _ = fmt.Fprintf(fw, `Windows Registry Editor Version 5.00

[HKEY_CLASSES_ROOT\Directory\Background\shell\fileServer]
@="File Server Here"
"Icon"="%s"

[HKEY_CLASSES_ROOT\Directory\Background\shell\fileServer\command]
@="\"%s\" -s \"%s\" -p \"%%V\""
`, strings.ReplaceAll(icoFile, "\\", "\\\\"),
		strings.ReplaceAll(execPath, "\\", "\\\\"), addr)
	return nil
}

/*--------------------------------Start 工具类---------------------------------*/
const (
	sortDirTypeByNameAsc sortDirType = iota
	sortDirTypeByNameDesc
	sortDirTypeByTimeAsc
	sortDirTypeByTimeDesc
	sortDirTypeBySizeAsc
	sortDirTypeBySizeDesc
	sortDirTypeByExtAsc
	sortDirTypeByExtDesc
)

type (
	sortDirType uint8
	dirInfoSort struct {
		fi       []os.FileInfo
		sortType sortDirType
	}
)

func sortDir(dir string, inputType *int) ([]os.FileInfo, error) {
	sortType := sortDirType(*inputType)
	if sortType < sortDirTypeByNameAsc || sortType > sortDirTypeByExtDesc {
		sortType = sortDirTypeByNameAsc
		*inputType = int(sortDirTypeByNameAsc)
	}
	f, err := os.Open(dir)
	if err != nil {
		return nil, err
	}
	list, err := f.Readdir(-1)
	_ = f.Close()
	if err != nil {
		return nil, err
	}
	sort.Sort(&dirInfoSort{fi: list, sortType: sortType})
	return list, nil
}

func (d *dirInfoSort) Len() int {
	return len(d.fi)
}

func (d *dirInfoSort) Default(x, y int) bool {
	lx, ly := len(d.fi[x].Name()), len(d.fi[y].Name())
	if lx == ly {
		return d.fi[x].Name() < d.fi[y].Name()
	}
	return lx < ly
}

func (d *dirInfoSort) Less(x, y int) bool {
	if d.fi[x].IsDir() != d.fi[y].IsDir() {
		return d.fi[x].IsDir() // 文件夹永远排在文件前面
	}
	switch d.sortType {
	default:
		fallthrough // 默认使用文件名升序
	case sortDirTypeByNameAsc:
		return d.Default(x, y)
	case sortDirTypeByNameDesc:
		lx, ly := len(d.fi[x].Name()), len(d.fi[y].Name())
		if lx == ly {
			return d.fi[x].Name() > d.fi[y].Name()
		}
		return lx > ly
	case sortDirTypeByTimeAsc:
		tx, ty := d.fi[x].ModTime(), d.fi[y].ModTime()
		if tx.Unix() == ty.Unix() {
			return d.Default(x, y)
		}
		return tx.Before(ty)
	case sortDirTypeByTimeDesc:
		tx, ty := d.fi[x].ModTime(), d.fi[y].ModTime()
		if tx.Unix() == ty.Unix() {
			return d.Default(x, y)
		}
		return tx.After(ty)
	case sortDirTypeBySizeAsc:
		sx, sy := d.fi[x].Size(), d.fi[y].Size()
		if sx == sy {
			return d.Default(x, y)
		}
		return sx < sy
	case sortDirTypeBySizeDesc:
		sx, sy := d.fi[x].Size(), d.fi[y].Size()
		if sx == sy {
			return d.Default(x, y)
		}
		return sx > sy
	case sortDirTypeByExtAsc:
		if !d.fi[x].IsDir() && !d.fi[y].IsDir() {
			return filepath.Ext(d.fi[x].Name()) < filepath.Ext(d.fi[y].Name())
		}
		return d.Default(x, y)
	case sortDirTypeByExtDesc:
		if !d.fi[x].IsDir() && !d.fi[y].IsDir() {
			return filepath.Ext(d.fi[x].Name()) > filepath.Ext(d.fi[y].Name())
		}
		return d.Default(x, y)
	}
}

func (d *dirInfoSort) Swap(x, y int) {
	d.fi[x], d.fi[y] = d.fi[y], d.fi[x]
}

var unitByte = []struct {
	byte float64
	unit string
}{
	{byte: 1},
	{byte: 1 << 10, unit: "B"},
	{byte: 1 << 20, unit: "KB"},
	{byte: 1 << 30, unit: "MB"},
	{byte: 1 << 40, unit: "GB"},
	{byte: 1 << 50, unit: "TB"},
}

func convertByte(buf []byte, b int64) string {
	tmp, unit := float64(b), "B"
	for i := 1; i < len(unitByte); i++ {
		if tmp < unitByte[i].byte {
			tmp /= unitByte[i-1].byte
			unit = unitByte[i].unit
			break
		}
	}
	return string(strconv.AppendFloat(buf, tmp, 'f', 2, 64)) + unit
}

func InternalIp() []string {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil
	}

	ips := make([]string, 0, len(interfaces))
	for _, inf := range interfaces {
		if inf.Flags&net.FlagUp != net.FlagUp ||
			inf.Flags&net.FlagLoopback == net.FlagLoopback {
			continue
		}

		addr, err := inf.Addrs()
		if err != nil {
			continue
		}

		for _, a := range addr {
			if ipNet, ok := a.(*net.IPNet); ok &&
				!ipNet.IP.IsLoopback() && ipNet.IP.To4() != nil {
				ips = append(ips, ipNet.IP.String())
			}
		}
	}
	return ips
}

type webErr struct {
	code int
	msg  string
}

func NewWebErr(msg string, code ...int) error {
	err := &webErr{code: http.StatusOK, msg: msg}
	if len(code) > 0 {
		err.code = code[0]
	}
	return err
}

func (w *webErr) Error() string {
	return w.msg
}

func String2Byte(s string) []byte {
	sh := (*reflect.StringHeader)(unsafe.Pointer(&s))
	bh := reflect.SliceHeader{Data: sh.Data, Len: sh.Len, Cap: sh.Len}
	return *(*[]byte)(unsafe.Pointer(&bh))
}

/*---------------------------------End 工具类----------------------------------*/

/*----------------------------Server Start 端代码------------------------------*/
var (
	htmlMsgPrefix = []byte("<html><head><title>message</title></head><body><center><h2>")
	htmlMsgSuffix = []byte("</h2></center></body></html>")
	respOk        = []byte("ok")
)

const (
	htmlTpl = `<html lang="zh"><head><title>list dir</title></head><body>
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
<tr><td>{{$v.Index}}</td><td>{{$v.Type}}</td><td>{{$v.Size}}</td><td>{{$v.Time}}</td><td><a href="{{$v.Href}}">{{$v.Name}}</a></td></tr>
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
}</script></body></html>`

	fileMode     = fs.FileMode(0666)
	headerType   = "Content-Type"
	urlencoded   = "application/x-www-form-urlencoded"
	janEncoded   = "application/janbar" // 使用本工具命令行的头
	headerLength = "Content-Length"
	contentRange = "Content-Range"
	janbarLength = "Janbar-Length"
	headPoint    = "Point" // 标识断点上传
	timeLayout   = "2006-01-02 15:04:05"
	encryptFlag  = "Encrypt" // header秘钥
)

func upDownFile(w http.ResponseWriter, r *http.Request) {
	var (
		err error
		buf = bytePool.Get().(*poolByte)
	)
	defer bytePool.Put(buf)

	switch r.Method {
	case http.MethodGet:
		err = handleGetFile(w, r, buf.buf)
	case http.MethodPost:
		err = handlePostFile(w, r, buf.buf)
	default:
		err = NewWebErr(r.Method + " not support")
	}
	if err != nil {
		e, ok := err.(*webErr)
		if !ok {
			e = &webErr{code: http.StatusInternalServerError, msg: err.Error()}
		}
		w.WriteHeader(e.code)
		w.Header().Set(headerType, "text/html;charset=utf-8")
		_, _ = w.Write(htmlMsgPrefix)
		_, _ = w.Write(String2Byte(e.msg))
		_, _ = w.Write(htmlMsgSuffix)
	}
}

// 渲染html模板需要的结构
type lineFileInfo struct {
	Index      int
	Type       string
	Size       string
	Time       string
	Href, Name string
}

func handleGetFile(w http.ResponseWriter, r *http.Request, buf []byte) error {
	path := filepath.Join(basePath, r.URL.Path)
	fi, err := os.Stat(path)
	if err != nil {
		return NewWebErr(path+" not found", http.StatusNotFound)
	}

	if r.Header.Get(headerType) == janEncoded {
		if fi.IsDir() {
			return NewWebErr("unable to get directory size")
		}
		// 获取服务器文件大小,用于断点上传文件
		size := string(strconv.AppendInt(buf[:0], fi.Size(), 10))
		w.Header().Set(janbarLength, size)
		//goland:noinspection HttpUrlsUsage
		_, _ = fmt.Fprintf(w, "curl -C %s --data-binary @file http://%s%s\n",
			size, r.Host, r.URL.Path)
		return nil
	}

	if fi.IsDir() {
		if useEncrypt != "" { // 加密方式不支持浏览目录,懒得写前端代码
			return NewWebErr("encrypt method not support list dir")
		}

		sortNum, _ := strconv.Atoi(r.FormValue("sort"))
		dir, err := sortDir(path, &sortNum) // 根据指定排序得到有序目录内容
		if err != nil {
			return err
		}

		info := make([]lineFileInfo, len(dir))
		for i, v := range dir {
			tmp := lineFileInfo{
				Index: i + 1,
				Size:  convertByte(buf[:0], v.Size()),
				Time:  string(v.ModTime().AppendFormat(buf[:0], timeLayout)),
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
		tpl, err := template.New("").Parse(htmlTpl)
		if err != nil {
			return err
		}
		err = tpl.Execute(w, map[string]interface{}{
			"sort": sortNum,
			"info": info,
		})
		if err != nil {
			return err
		}
	} else {
		// 尝试获取断点下载的位置,获取不到cur=0
		cur, _ := strconv.ParseInt(r.Header.Get(janbarLength), 10, 64)
		pw := handleWriteReadData(&handleData{cur: cur, ResponseWriter: w}, "GET > "+path, fi.Size())
		http.ServeFile(pw, r, path) // 支持断点下载
		pw.Close()
	}
	return nil
}

func handlePostFile(w http.ResponseWriter, r *http.Request, buf []byte) error {
	var (
		path      string
		size, cur int64
		fr        io.ReadCloser
		c         cipher.Stream

		fileFlag = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	)

	switch r.Header.Get(headerType) {
	case urlencoded:
		s, err := strconv.ParseInt(r.Header.Get(headerLength), 10, 0)
		if err != nil {
			return err
		}
		// 普通二进制上传文件,消息体直接是文件内容
		fr, size, path = r.Body, s, filepath.Join(basePath, r.URL.Path)
	case janEncoded:
		s, err := strconv.ParseInt(r.Header.Get(janbarLength), 10, 0)
		if err != nil {
			return err
		}

		// 判断是断点上传,则cur为断点位置
		cur, err = strconv.ParseInt(r.Header.Get(headPoint), 10, 64)
		if err == nil {
			fileFlag = os.O_CREATE | os.O_APPEND
		}
		// 本工具命令行上传文件
		fr, size, path = r.Body, s, filepath.Join(basePath, r.URL.Path)
	default:
		rf, rh, err := r.FormFile("file")
		if err != nil {
			return err
		}
		// 使用浏览器上传 或 curl -F "file=@C:\tmp.txt",这两种方式
		fr, size, path = rf, rh.Size, filepath.Join(basePath, r.URL.Path, rh.Filename)
	}
	//goland:noinspection GoUnhandledErrorResult
	defer fr.Close()

	fw, err := os.OpenFile(path, fileFlag, fileMode)
	if err != nil {
		return err
	}

	pw := handleWriteReadData(&handleData{
		cur:       cur,
		handle:    fw.Write,
		cipher:    c,
		hashAfter: true,
	}, "POST> "+path, size)
	_, err = io.CopyBuffer(pw, fr, buf)
	_ = fw.Close() // 趁早刷新缓存,因为要计算hash
	pw.Close()
	if err != nil {
		return err
	}
	_, err = w.Write(respOk)
	return err
}

type handleData struct {
	http.ResponseWriter

	cur       int64
	rate      chan int64
	sumHex    chan []byte
	pool      *poolByte
	hash      hash.Hash
	hashAfter bool // true表示加解密后数据计算hash
	cipher    cipher.Stream
	handle    func([]byte) (int, error)
}

func handleWriteReadData(p *handleData, prefix string, size int64) *handleData {
	if p.ResponseWriter != nil {
		// 这个是http服务的写入操作
		p.handle = p.ResponseWriter.Write
	}

	p.hash = md5.New()
	p.rate = make(chan int64)
	p.sumHex = make(chan []byte)
	p.pool = bytePool.Get().(*poolByte)
	go func() {
		pCur := "\r" + prefix + " %3d%%"
		for cur := range p.rate {
			fmt.Printf(pCur, cur*100/size)
		}
		fmt.Printf("\r%s 100%% %02x\n", prefix, <-p.sumHex)
		p.sumHex <- nil // 打印完成才能退出
	}()
	return p
}

func (p *handleData) add(n int) {
	p.cur += int64(n)
	select {
	case p.rate <- p.cur:
	default:
	}
}

func (p *handleData) grow(n int) []byte {
	if n > len(p.pool.buf) {
		p.pool.buf = make([]byte, n)
	}
	return p.pool.buf[:n] // 获取足够缓存
}

func (p *handleData) Write(b []byte) (n int, err error) {
	if p.cipher != nil {
		tmp := p.grow(len(b))
		n, err = p.handle(tmp)
		if p.hashAfter {
			// 使用解密后数据计算hash
			p.hash.Write(tmp[:n])
		} else {
			// 使用加密前数据计算hash
			p.hash.Write(b[:n])
		}
	} else if n, err = p.handle(b); n > 0 {
		p.hash.Write(b[:n])
	}
	p.add(n)
	time.Sleep(time.Millisecond * 50)
	return
}

func (p *handleData) Read(b []byte) (n int, err error) {
	if p.cipher != nil {
		tmp := p.grow(len(b))
		if n, err = p.handle(tmp); n > 0 {
			p.hash.Write(tmp[:n]) // 使用加密前数据计算hash
			p.cipher.XORKeyStream(b[:n], tmp[:n])
		}
	} else if n, err = p.handle(b); n > 0 {
		p.hash.Write(b[:n])
	}
	p.add(n)
	time.Sleep(time.Millisecond * 50)
	return
}

func (p *handleData) Close() {
	bytePool.Put(p.pool)
	close(p.rate)
	p.sumHex <- p.hash.Sum(nil)
	<-p.sumHex // 发送hash结果,确保打印结束
}

/*-----------------------------Server End 端代码-------------------------------*/

/*-----------------------------Client End 端代码-------------------------------*/
func clientMain() error {
	myFlag := flag.NewFlagSet(execPath+" cli", flag.ExitOnError)
	data := myFlag.String("d", "", "post data")
	output := myFlag.String("o", "", "output")
	point := myFlag.Bool("c", false, "Resumed transfer offset")
	myFlag.StringVar(&useEncrypt, "e", "", "encrypt data")
	_ = myFlag.Parse(os.Args[2:])

	httpUrl := myFlag.Arg(0)
	if httpUrl == "" {
		return NewWebErr("url is null")
	}

	buf := bytePool.Get().(*poolByte)
	defer bytePool.Put(buf)
	if *data != "" {
		return clientPost(*data, httpUrl, *point, buf.buf)
	}
	return clientGet(httpUrl, *output, *point, buf.buf)
}

// 获取服务器文件大小,用于断点上传功能
func clientHead(url string) (int64, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set(headerType, janEncoded)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	//goland:noinspection GoUnhandledErrorResult
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return 0, nil // 服务器没有文件
	}
	return strconv.ParseInt(resp.Header.Get(janbarLength), 10, 0)
}

func clientPost(data, url string, point bool, buf []byte) error {
	var (
		size, cur int64
		key       string
		path      string
		body      io.Reader
		c         cipher.Stream
		err       error
	)
	if useEncrypt != "" {
	}

	if len(data) > 1 && data[0] == '@' {
		if point {
			// 断点上传,获取服务器文件大小
			cur, err = clientHead(url)
			if err != nil {
				return err
			}
		}

		path = data[1:]
		fr, err := os.Open(path)
		if err != nil {
			return err
		}
		//goland:noinspection GoUnhandledErrorResult
		defer fr.Close()

		fi, err := fr.Stat()
		if err != nil {
			return err
		}
		size = fi.Size()

		if cur > 0 {
			if cur == size {
				return NewWebErr("file upload is complete")
			}

			// 断点上传时,将文件定位到指定位置
			_, err = fr.Seek(cur, io.SeekStart)
			if err != nil {
				return err
			}
		}
		body = fr
	} else {
		sr := strings.NewReader(data)
		size, path, body = sr.Size(), "string data", sr
	}

	pr := handleWriteReadData(&handleData{
		cur:    cur,
		handle: body.Read,
		cipher: c,
	}, "POST> "+path, size)
	defer pr.Close()

	req, err := http.NewRequest(http.MethodPost, url, pr)
	if err != nil {
		return err
	}

	req.Header.Set(headerType, janEncoded) // 表示使用工具上传
	req.Header.Set(janbarLength, string(strconv.AppendInt(buf[:0], size, 10)))
	if point {
		// 告诉服务器断点续传的上传数据
		req.Header.Set(headPoint, string(strconv.AppendInt(buf[:0], cur, 10)))
	}
	if key != "" {
		// 告诉服务器,加密通信
		req.Header.Set(encryptFlag, key)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	if resp.Body != nil {
		if resp.StatusCode != http.StatusOK {
			_, _ = io.CopyBuffer(os.Stdout, resp.Body, buf)
		} else {
			_, _ = io.CopyBuffer(io.Discard, resp.Body, buf)
		}
		//goland:noinspection GoUnhandledErrorResult
		resp.Body.Close()
	}
	return nil
}

func clientGet(url, output string, point bool, buf []byte) error {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	if output == "" {
		output = filepath.Base(req.URL.Path)
	}

	fileFlag := os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	fi, err := os.Stat(output)
	if err == nil {
		if fi.IsDir() {
			return NewWebErr(output + "is dir")
		}
		if point {
			fileFlag = os.O_CREATE | os.O_APPEND
			sSize := string(strconv.AppendInt(buf[:0], fi.Size(), 10))
			// 断点续传,设置规定的header,服务器负责解析并处理
			req.Header.Set("Range", "bytes="+sSize+"-")
			req.Header.Set(janbarLength, sSize) // 告诉服务器,从哪个位置下载
		}
	}
	fw, err := os.OpenFile(output, fileFlag, fileMode)
	if err != nil {
		return err
	}
	//goland:noinspection GoUnhandledErrorResult
	defer fw.Close()

	var c cipher.Stream
	if useEncrypt != "" { // 客户端将随机秘钥发到服务器
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	if resp.Body == nil {
		return NewWebErr("body is null")
	}
	//goland:noinspection GoUnhandledErrorResult
	defer resp.Body.Close()

	var size, cur int64
	switch resp.StatusCode {
	case http.StatusOK: // 刚开始下载
		size, err = strconv.ParseInt(resp.Header.Get(headerLength), 10, 64)
		if err != nil {
			return err
		}
	case http.StatusPartialContent:
		var length int64 // 断点续传,从header中获取位置和总大小
		_, err = fmt.Sscanf(resp.Header.Get(contentRange),
			"bytes %d-%d/%d", &cur, &length, &size)
		if err != nil {
			return err
		}
	case http.StatusRequestedRangeNotSatisfiable:
		// 已经下载完毕,无需重复下载
		size, _ = io.CopyBuffer(io.Discard, resp.Body, buf)
		fmt.Printf("[%d bytes data]\n", size)
		return nil
	default:
		fmt.Printf("StatusCode:%d\n", resp.StatusCode)
		_, _ = io.CopyBuffer(os.Stdout, resp.Body, buf)
		return nil // 打印错误
	}

	pw := handleWriteReadData(&handleData{
		cur:       cur,
		handle:    fw.Write,
		cipher:    c,
		hashAfter: true,
	}, "GET > "+output, size)
	_, err = io.CopyBuffer(pw, resp.Body, buf)
	pw.Close()
	return err
}

/*----------------------------Client Start 端代码------------------------------*/
