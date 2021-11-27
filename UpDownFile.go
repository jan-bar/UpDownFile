package main

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"crypto/rand"
	_ "embed"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"hash"
	"html/template"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	timeLayout   = "2006-01-02 15:04:05"
	encryptFlag  = "Encrypt"
	headerLength = "Content-Length"
	janbarLength = "Janbar-Length"
	headMethod   = "Head"
	headPoint    = "Point"
	headerType   = "Content-Type"
	urlencoded   = "application/x-www-form-urlencoded"
	limitKeyTime = 120
)

var execPath string

//goland:noinspection HttpUrlsUsage
func main() {
	var err error // 获取程序运行路径
	execPath, err = os.Executable()
	if err != nil {
		panic(err)
	}

	if len(os.Args) > 2 && os.Args[1] == "cli" {
		if err = clientMain(); err != nil {
			panic(err)
		}
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
		for _, v := range InternalIp() {
			urls = append(urls, v+":"+port)
		}
		addrStr = "127.0.0.1:" + port
	}
	urls = append(urls, addrStr)

	basePath, err = filepath.Abs(basePath)
	if err != nil {
		panic(err)
	}

	tpl, err := template.New("").Parse(`{{range $i,$v := .urls}}
url: "http://{{$v}}"
{{- end}}

server:
    {{.exec}} -s {{.addr}} -p {{.dir}} -t {{.timeout}}{{if .pass}} -e password{{end}}
cli get:
    {{.exec}} cli -u "http://{{.addr}}/tmp.txt" -c{{if .pass}} -e password{{end}}
cli post:
    {{.exec}} cli -d @C:\tmp.txt -u "http://{{.addr}}/tmp.txt" -c{{if .pass}} -e password{{end}}

GET file:
    wget -c --content-disposition "http://{{.addr}}/tmp.txt"
    curl -C - -OJ "http://{{.addr}}/tmp.txt"
POST file:
    wget -qO - --post-file=C:\tmp.txt "http://{{.addr}}/tmp.txt"
    curl --data-binary @C:\tmp.txt "http://{{.addr}}/tmp.txt"
    curl -F "file=@C:\tmp.txt" "http://{{.addr}}/"
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
		"pass":    useEncrypt != "",
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
		return nil
	}

	fw, err := os.Create("addRightClickRegistry.reg")
	if err != nil {
		return err
	}
	//goland:noinspection GoUnhandledErrorResult
	defer fw.Close()

	icoFile := filepath.Join(filepath.Dir(execPath), "fileServer.ico")
	err = ioutil.WriteFile(icoFile, icoData, 0666)
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

type poolByte struct{ buf []byte } // 这种方式才能过语法检查

var (
	bytePool = sync.Pool{New: func() interface{} {
		return &poolByte{buf: make([]byte, 32<<10)}
	}}

	//go:embed fileServer.ico
	icoData []byte

	basePath   string
	useEncrypt string

	errCheckOk = errors.New("check header")
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
		err = errors.New(r.Method + " not support")
	}
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		w.Header().Set(headerType, "text/html;charset=utf-8")
		_, _ = fmt.Fprintf(w, "%s%+v%s", htmlMsgPrefix, err, htmlMsgSuffix)
	}
}

func httpGetStream(key string, check bool) (cipher.Stream, error) {
	if useEncrypt != "" { // 服务器启用秘钥
		c, err := newDecrypt(key)
		if err != nil {
			return nil, err
		}
		if check { // 检查key成功,上层用来判断
			return nil, errCheckOk
		}
		return c, nil
	}
	if key != "" { // 未启用秘钥时,客户端发送了秘钥则提示不支持
		return nil, errors.New("server not support encrypt data")
	}
	return nil, nil
}

func handlePostFile(w http.ResponseWriter, r *http.Request, buf []byte) error {
	var (
		path string
		size int64
		fr   io.ReadCloser
		c    cipher.Stream

		fileFlag = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	)
	if r.Header.Get(headerType) == urlencoded {
		var err error
		c, err = httpGetStream(r.Header.Get(encryptFlag), r.Header.Get(headMethod) == "check")
		if err != nil {
			if err == errCheckOk {
				return nil // 返回客户端key正确
			}
			return err
		}

		s, err := strconv.ParseInt(r.Header.Get(headerLength), 10, 0)
		if err != nil { // go库会删掉headerLength
			s, err = strconv.ParseInt(r.Header.Get(janbarLength), 10, 0)
			if err != nil {
				return err
			}
		}
		// 服务器收到客户端是断点上传的文件
		if r.Header.Get(headPoint) == "true" {
			fileFlag = os.O_CREATE | os.O_APPEND
		}
		fr, size, path = r.Body, s, filepath.Join(basePath, r.URL.Path)
	} else {
		rf, rh, err := r.FormFile("file")
		if err != nil {
			return err
		}
		fr, size, path = rf, rh.Size, filepath.Join(basePath, r.URL.Path, rh.Filename)
	}
	//goland:noinspection GoUnhandledErrorResult
	defer fr.Close()

	fw, err := os.OpenFile(path, fileFlag, 0666)
	if err != nil {
		return err
	}

	pw := handleWriteReadData(&handleData{
		handle:     fw.Write,
		cipher:     c,
		hashMethod: hashAfter,
	}, "POST>"+path, size)
	_, err = io.CopyBuffer(pw, fr, buf)
	_ = fw.Close() // 趁早刷新缓存
	pw.Close()
	if err != nil {
		return err
	}
	_, err = w.Write(respOk)
	return err
}

func handleGetFile(w http.ResponseWriter, r *http.Request, buf []byte) error {
	path := filepath.Join(basePath, r.URL.Path)
	fi, err := os.Stat(path)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		return err
	}

	headStr := r.Header.Get(headMethod)
	if fi.IsDir() {
		if headStr != "" {
			return errors.New("head not support list dir")
		}
		if useEncrypt != "" { // 加密方式不支持浏览目录,懒得写前端代码
			return errors.New("encrypt method not support list dir")
		}

		tmpInt, _ := strconv.Atoi(r.FormValue("sort"))
		dir, err := sortDir(path, &tmpInt)
		if err != nil {
			return err
		}
		// 找到对应位置插入checked字段
		tmpInt = 11 + bytes.Index(htmlPrefix, append(buf[:0], "sortDir("+strconv.Itoa(tmpInt)...))
		_, _ = w.Write(htmlPrefix[:tmpInt])
		_, _ = w.Write(htmlChecked) // 加入默认被选中
		_, _ = w.Write(htmlPrefix[tmpInt:])

		link := bytes.NewBuffer(buf[1024:])
		for i, v := range dir {
			_, _ = w.Write(htmlTrTd)
			_, _ = w.Write(strconv.AppendInt(buf[:0], int64(i+1), 10))
			_, _ = w.Write(htmlTdTd)

			link.Reset()
			link.WriteString(url.PathEscape(v.Name()))
			if v.IsDir() {
				_, _ = w.Write(htmlDir)
				link.WriteByte('/')
			} else {
				_, _ = w.Write(htmlFile)
			}

			_, _ = w.Write(htmlTdTd)
			_, _ = w.Write(convertByte(buf[:0], v.Size()))
			_, _ = w.Write(htmlTdTd)
			_, _ = w.Write(v.ModTime().AppendFormat(buf[:0], timeLayout))
			_, _ = w.Write(htmlTdTdA)
			_, _ = w.Write(link.Bytes())
			_, _ = w.Write(htmlGt)
			_, _ = w.Write([]byte(v.Name()))
			_, _ = w.Write(htmlAtdTr)
		}
		_, _ = w.Write(htmlSuffix)
	} else if headStr == "check" {
		// 返回服务器当前文件大小,用于断点上传,可用curl进行断点上传
		size := string(strconv.AppendInt(buf[:0], fi.Size(), 10))
		w.Header().Set(janbarLength, size)
		_, _ = w.Write([]byte("curl -C " + size + " --data-binary @file url\n"))
	} else {
		c, err := httpGetStream(r.Header.Get(encryptFlag), false)
		if err != nil {
			return err
		}
		pw := handleWriteReadData(&handleData{
			handle:      w.Write,
			header:      w.Header(),
			writeHeader: w.WriteHeader,
			cipher:      c,
			hashMethod:  hashBefore,
		}, "GET >"+path, fi.Size())
		// 使用go库的文件服务器,支持断点续传,以及各种处理
		http.ServeFile(pw, r, path)
		pw.Close()
	}
	return nil
}

var (
	htmlMsgPrefix = []byte("<html><head><title>message</title></head><body><center><h2>")
	htmlMsgSuffix = []byte("</h2></center></body></html>")
	respOk        = []byte("ok")

	htmlTrTd    = []byte("<tr><td>")
	htmlDir     = []byte{'D'}
	htmlFile    = []byte{'F'}
	htmlTdTd    = []byte("</td><td>")
	htmlTdTdA   = []byte("</td><td><a href=\"")
	htmlGt      = []byte("\">")
	htmlAtdTr   = []byte("</a></td></tr>")
	htmlChecked = []byte(" checked")
	htmlPrefix  = []byte(`<html lang="zh"><head><title>list dir</title></head><body><div style="position:fixed;bottom:20px;right:10px">
<p><label><input type="radio" name="sort" onclick="sortDir(0)">名称升序</label><label><input type="radio" name="sort" onclick="sortDir(1)">名称降序</label></p>
<p><label><input type="radio" name="sort" onclick="sortDir(2)">时间升序</label><label><input type="radio" name="sort" onclick="sortDir(3)">时间降序</label></p>
<p><label><input type="radio" name="sort" onclick="sortDir(4)">大小升序</label><label><input type="radio" name="sort" onclick="sortDir(5)">大小降序</label></p>
<p><label><input type="radio" name="sort" onclick="sortDir(6)">后缀升序</label><label><input type="radio" name="sort" onclick="sortDir(7)">后缀降序</label></p>
<p><input type="file" id="file"></p><progress value="0" id="progress"></progress><p><input type="button" onclick="uploadFile()" value="上传文件"></p><input type="button" onclick="backSuper()" value="返回上级"/>
<a href="#top" style="margin:5px">顶部</a><a href="#bottom">底部</a></div><table border="1" align="center"><tr><th>序号</th><th>类型</th><th>大小</th><th>修改时间</th><th>链接</th></tr>`)
	htmlSuffix = []byte(`</table><a name="bottom"></a><script>
function uploadFile() {
    let upload = document.getElementById('file').files[0]
    if (!upload) {
        alert('请选择上传文件')
        return
    }
    let params = new FormData()
    params.append('file', upload)
    let xhr = new XMLHttpRequest()
    xhr.onerror = function () {
        alert('请求失败')
    }
    xhr.onreadystatechange = function () {
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
    xhr.upload.onprogress = function (e) {
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
    for (; i >= 0 && url[i] === '/'; i--) {}
    for (; i >= 0 && url[i] !== '/'; i--) {}
    window.location.href = window.location.origin + url.substring(0, i + 1)
}
</script></body></html>`)
)

/*--------------------------------下面是客户端---------------------------------*/
func clientMain() error {
	fs := flag.NewFlagSet(execPath+" cli", flag.ExitOnError)
	httpUrl := fs.String("u", "", "http url")
	data := fs.String("d", "", "post data")
	output := fs.String("o", "", "output")
	point := fs.Bool("c", false, "Resumed transfer offset")
	fs.StringVar(&useEncrypt, "e", "", "encrypt data")
	_ = fs.Parse(os.Args[2:])

	if *httpUrl == "" {
		return errors.New("url is null")
	}

	buf := bytePool.Get().(*poolByte)
	defer bytePool.Put(buf)
	if *data != "" {
		return clientPost(*data, *httpUrl, *point, buf.buf)
	}
	return clientGet(*httpUrl, *output, *point, buf.buf)
}

// 获取服务器文件大小,主要用于断点上传功能
func clientHead(url string) (int64, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set(headMethod, "check")
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

// http post客户端,支持断点上传
func clientPost(data, url string, point bool, buf []byte) error {
	var (
		size int64
		key  string
		path string
		body io.Reader
		c    cipher.Stream
		err  error
	)
	if useEncrypt != "" { // 加密上传数据
		key, c, err = newEncrypt(buf)
		if err != nil {
			return err
		}
		req, err := http.NewRequest(http.MethodPost, url, nil)
		if err != nil {
			return err
		}
		req.Header.Set(headerType, urlencoded)
		req.Header.Set(headMethod, "check")
		req.Header.Set(encryptFlag, key)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return err
		}
		if resp.StatusCode != http.StatusOK {
			_, err = io.CopyBuffer(os.Stdout, resp.Body, buf)
			if err != nil {
				return err
			}

			return resp.Body.Close()
		}
		_ = resp.Body.Close()
	}

	if len(data) >= 1 && data[0] == '@' {
		if point { // 断点上传
			size, err = clientHead(url)
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
		if size == fi.Size() {
			return errors.New("file upload is complete")
		}

		if size > 0 {
			_, err = fr.Seek(size, io.SeekStart)
			if err != nil {
				return err
			}
		}
		size, body = fi.Size()-size, fr
	} else {
		sr := strings.NewReader(data)
		size, path, body = sr.Size(), "string data", sr
	}

	pr := handleWriteReadData(&handleData{
		handle: body.Read,
		cipher: c,
	}, "POST>"+path, size)
	defer pr.Close()

	req, err := http.NewRequest(http.MethodPost, url, pr)
	if err != nil {
		return err
	}
	req.Header.Set(headerType, urlencoded)
	req.Header.Set(janbarLength, string(strconv.AppendInt(buf[:0], size, 10)))
	if point { // 告诉服务器断点续传的上传数据
		req.Header.Set(headPoint, "true")
	}
	if key != "" {
		req.Header.Set(encryptFlag, key)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	if resp.Body != nil {
		if resp.StatusCode != http.StatusOK {
			_, err = io.CopyBuffer(os.Stdout, resp.Body, buf)
		} else {
			_, err = io.CopyBuffer(ioutil.Discard, resp.Body, buf)
		}
		if err != nil {
			return err
		}
		_ = resp.Body.Close()
	}
	return nil
}

// http get客户端,支持断点下载
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
			return errors.New(output + "is dir")
		}
		if point { // 断点续传
			fileFlag = os.O_APPEND
			req.Header.Set("Range", "bytes="+string(strconv.AppendInt(buf[:0], fi.Size(), 10))+"-")
		}
	}
	fw, err := os.OpenFile(output, fileFlag, 0666)
	if err != nil {
		return err
	}
	//goland:noinspection GoUnhandledErrorResult
	defer fw.Close()

	var c cipher.Stream
	if useEncrypt != "" { // 客户端将随机秘钥发到服务器
		var key string
		key, c, err = newEncrypt(buf)
		if err != nil {
			return err
		}
		req.Header.Set(encryptFlag, key)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	if resp.Body == nil {
		return errors.New("body is null")
	}
	//goland:noinspection GoUnhandledErrorResult
	defer resp.Body.Close()

	var size int64
	switch resp.StatusCode {
	case http.StatusOK, http.StatusPartialContent: // 完整接收,断点续传
	case http.StatusRequestedRangeNotSatisfiable:
		size, _ = io.CopyBuffer(ioutil.Discard, resp.Body, buf)
		fmt.Printf("[%d bytes data]\n", size) // 已经下载完毕
		return nil
	default:
		fmt.Printf("StatusCode:%d\n", resp.StatusCode)
		_, err = io.CopyBuffer(os.Stdout, resp.Body, buf)
		return err // 打印错误
	}

	size, err = strconv.ParseInt(resp.Header.Get(headerLength), 10, 0)
	if err != nil {
		return err
	}

	pw := handleWriteReadData(&handleData{
		handle:     fw.Write,
		cipher:     c,
		hashMethod: hashAfter,
	}, "GET >"+output, size)
	_, err = io.CopyBuffer(pw, resp.Body, buf)
	pw.Close()
	return err
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
	sortDirType int
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

func convertByte(buf []byte, b int64) []byte {
	tmp, unit := float64(b), "B"
	for i := 1; i < len(unitByte); i++ {
		if tmp < unitByte[i].byte {
			tmp /= unitByte[i-1].byte
			unit = unitByte[i].unit
			break
		}
	}
	return append(strconv.AppendFloat(buf, tmp, 'f', 2, 64), unit...)
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

/*---------------------------------End 工具类----------------------------------*/

type (
	hashMethod uint8

	handleData struct {
		cnt    int64
		rate   chan int64
		header http.Header
		buf    *poolByte
		tmp    []byte
		cipher cipher.Stream

		hash        hash.Hash
		hashMethod  hashMethod
		writeHeader func(int)
		handle      func([]byte) (int, error)
	}
)

const (
	hashBefore hashMethod = iota
	hashAfter
)

func handleWriteReadData(p *handleData, prefix string, size int64) *handleData {
	if p.hash == nil {
		p.hash = md5.New()
	}
	p.rate = make(chan int64)
	p.buf = bytePool.Get().(*poolByte)
	go func(rate <-chan int64, prefix string, size int64, h hash.Hash) {
		pCur := "\r" + prefix + " %3d%%"
		for cur := range rate {
			fmt.Printf(pCur, cur*100/size)
		}
		fmt.Println("\r" + prefix + " 100% " + toHexStr(h.Sum(nil)))
	}(p.rate, prefix, size, p.hash)
	return p
}

func toHexStr(src []byte) string {
	const hexTable = "0123456789abcdef"
	str := new(strings.Builder)
	str.Grow(2 * len(src))
	for _, v := range src {
		str.WriteByte(hexTable[v>>4])
		str.WriteByte(hexTable[v&0xf])
	}
	return str.String()
}

func (p *handleData) Header() http.Header { return p.header }
func (p *handleData) WriteHeader(code int) {
	if p.writeHeader != nil {
		p.writeHeader(code)
	}
}
func (p *handleData) add(n int) {
	p.cnt += int64(n)
	select {
	case p.rate <- p.cnt:
	default:
	}
}
func (p *handleData) Write(b []byte) (n int, err error) {
	if p.cipher != nil {
		p.tmp = genByte(p.buf.buf, len(b))
		p.cipher.XORKeyStream(p.tmp, b)
		n, err = p.handle(p.tmp)
		if p.hashMethod == hashAfter {
			p.hash.Write(p.tmp[:n]) // 使用解密后数据计算hash
		} else {
			p.hash.Write(b[:n]) // 使用加密前数据计算hash
		}
	} else {
		n, err = p.handle(b)
		p.hash.Write(b[:n])
	}
	p.add(n)
	return
}
func (p *handleData) Read(b []byte) (n int, err error) {
	if p.cipher != nil {
		p.tmp = genByte(p.buf.buf, len(b))
		if n, err = p.handle(p.tmp); n > 0 {
			p.hash.Write(p.tmp[:n]) // 使用加密前数据计算hash
			p.cipher.XORKeyStream(b[:n], p.tmp[:n])
		}
	} else {
		n, err = p.handle(b)
		p.hash.Write(b[:n])
	}
	p.add(n)
	return
}
func (p *handleData) Close() {
	close(p.rate)
	time.Sleep(time.Millisecond) // 等打印协程打印完
	bytePool.Put(p.buf)
}

// 缓存够就用缓存,缓存不够产生新的对象
func genByte(buf []byte, n int) []byte {
	tmp := buf
	if n > len(tmp) {
		return make([]byte, n)
	}
	return tmp[:n]
}

/*--------------------------------加密工具类---------------------------------*/
// 生成随机秘钥,并返回加密对象
func newEncrypt(buf []byte) (string, cipher.Stream, error) {
	tmp := genByte(buf, 32)
	_, err := rand.Read(tmp[8:30])
	if err != nil {
		return "", nil, err
	}
	setGetInt64(tmp, time.Now().Unix())  // 将时间戳存进去
	tmp[30], tmp[31] = calcCrc(tmp[:30]) // 存入2byte的crc

	dst := genByte(buf[64:], 32)
	if err = encryptKey(dst, tmp); err != nil {
		return "", nil, err
	}
	return base64.StdEncoding.EncodeToString(dst), newRc4Cipher(tmp), nil
}

// 根据秘钥返回解密对象
func newDecrypt(key string) (cipher.Stream, error) {
	if key == "" {
		return nil, errors.New("key is empty")
	}
	buf, err := base64.StdEncoding.DecodeString(key)
	if err != nil {
		return nil, err
	}
	dst := make([]byte, len(buf))
	if err = encryptKey(dst, buf); err != nil {
		return nil, err
	}
	if len(dst) == 32 {
		c0, c1 := calcCrc(dst[:30])
		if c0 == dst[30] && c1 == dst[31] {
			if abs(time.Now().Unix()-setGetInt64(dst, -1)) < limitKeyTime {
				return newRc4Cipher(dst), nil
			}
		}
	}
	return nil, errors.New("key decrypt error")
}

func abs(d int64) int64 {
	if d < 0 {
		return -d
	}
	return d
}

func setGetInt64(b []byte, data int64) int64 {
	loop := [...]int{0, 8, 16, 24, 32, 40, 48, 56}
	if data >= 0 { // 将data存入b
		u := uint64(data)
		for i, v := range loop {
			b[i] = byte(u >> v)
		}
	} else { // 从b中得出data
		var u uint64
		for i, v := range loop {
			u |= uint64(b[i]) << v
		}
		data = int64(u)
	}
	return data
}

func calcCrc(buf []byte) (byte, byte) {
	c := uint16(0xffff)
	for _, v := range buf {
		c ^= uint16(v)
		for i := 0; i < 8; i++ {
			if (c & 1) == 1 {
				c = (c >> 1) ^ 0xa001
			} else {
				c >>= 1
			}
		}
	}
	return byte(c), byte(c >> 8)
}

func encryptKey(dst, src []byte) error {
	var (
		n, kLen = 0, 32
		tmp     = genByte(dst, kLen)
	)
	for n < kLen {
		n += copy(tmp[n:], useEncrypt)
	}
	block, err := aes.NewCipher(tmp)
	if err != nil {
		return err
	}
	n, kLen = 0, block.BlockSize()
	tmp = genByte(dst, kLen)
	for n < kLen {
		n += copy(tmp[n:], useEncrypt)
	}
	cipher.NewCTR(block, tmp).XORKeyStream(dst, src)
	return nil
}

type rc4Cipher struct {
	s    [256]uint32
	x, y uint32

	i, j, i0, j0, tmp uint8
}

func newRc4Cipher(key []byte) cipher.Stream {
	c := new(rc4Cipher)
	for i := uint32(0); i < 256; i++ {
		c.s[i] = i
	}
	// 初始变量需要做好赋值
	c.i, c.j, c.j0 = 0, 0, 0
	l := len(key)
	for i := 0; i < 256; i++ {
		c.j0 += uint8(c.s[i]) + key[i%l]
		c.s[i], c.s[c.j0] = c.s[c.j0], c.s[i]
	}
	c.tmp = uint8(c.s[key[0]])
	return c
}

func (c *rc4Cipher) XORKeyStream(dst, src []byte) {
	c.i0, c.j0 = c.i, c.j
	for k, v := range src {
		c.i0++
		c.x = c.s[c.i0]
		c.j0 += uint8(c.x)
		c.y = c.s[c.j0]
		c.s[c.i0], c.s[c.j0] = c.y, c.x
		dst[k] = v ^ uint8(c.s[uint8(c.x+c.y)]) ^ c.tmp
	}
	c.i, c.j = c.i0, c.j0
}
