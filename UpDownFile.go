package main

import (
    "bufio"
    "encoding/base64"
    "errors"
    "fmt"
    "io"
    "io/ioutil"
    "net"
    "net/url"
    "os"
    "path/filepath"
    "strconv"
    "strings"
)

var authStr string // 授权信息

func main() {
    var path, user, pass string
    switch len(os.Args) {
    case 2: // 当前目录
        path = "."
    case 3: // 无认证模式
        path = os.Args[2]
    case 5: // 添加用户名密码认证
        path, user, pass = os.Args[2], os.Args[3], os.Args[4]
        authStr = "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pass))
    default:
        fmt.Printf("usage: %s ip:port [path] [user] [pass]\n", os.Args[0])
        return
    }

    addr, err := net.ResolveTCPAddr("tcp", os.Args[1])
    if err != nil {
        panic(err)
    }
    path, err = filepath.Abs(path)
    if err != nil {
        return
    }

    ser, err := net.ListenTCP("tcp", addr)
    if err != nil {
        panic(err)
    }

    if authStr != "" {
        fmt.Printf(`get file:
  wget --auth-no-challenge --user=%s --password=%s --content-disposition "http://%s?path"
  curl -u %s:%s -OJ "http://%s?path"
post file:
  wget -qO - --auth-no-challenge --user=%s --password=%s --post-file=C:\file "http://%s?path"
  curl -u %s:%s --data-binary @C:\file "http://%s?path"
`, user, pass, addr, user, pass, addr, user, pass, addr, user, pass, addr)
    } else {
        fmt.Printf(`get file:
  wget --content-disposition "http://%s?path"
  curl -OJ "http://%s?path"
post file:
  wget -qO - --post-file=C:\file "http://%s?path"
  curl --data-binary @C:\file "http://%s?path"
`, addr, addr, addr, addr)
    }
    for {
        ln, err := ser.AcceptTCP()
        if err != nil {
            panic(err)
        }
        go func(l *net.TCPConn) {
            h := &httpHandle{
                path:   path,
                header: make(map[string]string, 4),
                r:      bufio.NewReader(l),
                w:      bufio.NewWriter(l),
            }
            err := handleFile(h)
            if err != nil {
                h.respMessage(err.Error())
            }
            h.w.Flush()
            l.Close()
        }(ln)
    }
}

const (
    httpGet   = "GET"
    httpPost  = "POST"
    resHeader = "HTTP/1.1 200 OK\r\nContent-Type:text/html;charset=utf-8\r\n\r\n"
    getHeader = "HTTP/1.1 200 OK\r\nContent-Type:application/octet-stream\r\nContent-Disposition:attachment;filename=%s\r\nContent-Length:%d\r\nContent-Transfer-Encoding:binary\r\n\r\n"
)

func handleFile(h *httpHandle) error {
    err := h.getHeader()
    if err != nil {
        return err
    }
    if h.method == httpGet {
        return h.get()
    }
    return h.post()
}

type httpHandle struct {
    path    string
    method  string
    urlPath string
    header  map[string]string
    r       *bufio.Reader
    w       *bufio.Writer
}

func (h *httpHandle) respMessage(data string) {
    h.w.WriteString("HTTP/1.1 200 OK\r\nContent-Type:text/html;charset=utf-8\r\n\r\n<html><head><title>message</title></head><body><center><h2>")
    h.w.WriteString(data)
    h.w.WriteString("</h2></center></body></html>")
}

func (h *httpHandle) getHeader() error {
    // 读取第一行,提取有用信息
    line, _, err := h.r.ReadLine()
    if err != nil {
        return err
    }
    header := strings.Fields(string(line))
    if len(header) < 3 { // 首行至少3列数据
        return errors.New("header error")
    }
    h.method = header[0]
    if h.method != httpGet && h.method != httpPost {
        return errors.New(h.method + " not support")
    }
    u, err := url.Parse(header[1])
    if err != nil {
        return err
    }
    h.urlPath = u.Path

    for {
        line, _, err = h.r.ReadLine()
        if err != nil {
            return err
        }
        if len(line) == 0 {
            break // 遇到空行,之后为请求体
        }
        tmp := string(line)
        if index := strings.Index(tmp, ":"); index > 0 { // key: val
            h.header[strings.ToLower(strings.TrimSpace(tmp[:index]))] = strings.TrimSpace(tmp[index+1:])
        }
    }
    return nil
}

func (h *httpHandle) get() error {
    path := filepath.Join(h.path, h.urlPath)
    fi, err := os.Stat(path)
    if err != nil {
        return err
    }

    if fi.IsDir() {
        dir, err := ioutil.ReadDir(path)
        if err != nil {
            return err
        }
        h.w.WriteString("HTTP/1.1 200 OK\r\nContent-Type:text/html;charset=utf-8\r\n\r\n<html><head><title>list dir</title></head><body>")
        h.w.WriteString(`<div style="position:fixed;bottom:20px;right:20px">
<form action="` + h.urlPath + `" method="POST" enctype="multipart/form-data">
    <p><input type="file" name="file"></p>
    <p><input type="submit" value="上传文件"></p>
</form>
<input type="button" onclick="javascript:window.history.back()" value="后退"/>
<input type="button" onclick="javascript:window.history.forward()" value="前进" style="margin:5px"/>
<a href="#top" style="margin:5px">顶部</a>
<a href="#bottom">底部</a>
</div>`)
        h.w.WriteString("<table border=\"1\" align=\"center\"><tr><th>类型</th><th>大小</th><th>修改时间</th><th>链接</th></tr>")
        for _, v := range dir {
            h.w.WriteString("<tr><td>")
            if v.IsDir() {
                h.w.WriteByte('D')
            } else {
                h.w.WriteByte('F')
            }
            h.w.WriteString("</td><td>")
            h.w.WriteString(convertByte(v.Size()))
            h.w.WriteString("</td><td>")
            h.w.WriteString(v.ModTime().Format("2006-01-02 15:04:05"))
            h.w.WriteString("</td><td><a href=\"")
            // 对文件或目录进行拼接
            h.w.WriteString(url.QueryEscape(strings.TrimLeft(h.urlPath+"/"+v.Name(), "/")))
            h.w.WriteString("\">")
            h.w.WriteString(v.Name())
            h.w.WriteString("</a></td></tr>")
        }
        h.w.WriteString("</table><a name=\"bottom\"></a></body></html>")
        return nil
    }

    size := fi.Size()
    fr, err := os.Open(path)
    if err != nil {
        return err
    }
    fmt.Fprintf(h.w, getHeader, filepath.Base(path), size)
    pr := newProgress(fr, path, size)
    _, err = io.Copy(h.w, pr)
    pr.Close()
    fr.Close()
    return err
}

func (h *httpHandle) post() error {
    var size int64
    if tmp, ok := h.header["content-length"]; ok {
        size, _ = strconv.ParseInt(tmp, 10, 0)
    }
    if size <= 0 {
        return errors.New("content-length error")
    }
    var boundary string
    if tmp, ok := h.header["content-type"]; ok {
        for _, v := range strings.Split(tmp, ";") {
            if v = strings.TrimSpace(v); v == "multipart/form-data" {
                ok = false
            } else if strings.HasPrefix(v, "boundary=") {
                boundary = "--" + v[9:]
            }
        }
        if ok { // 没有multipart/form-data则置空
            boundary = ""
        }
    }
    if boundary == "" {
        return errors.New("content-type error")
    }

    line, _, err := h.r.ReadLine()
    if err != nil {
        return err
    }
    if string(line) != boundary {
        return errors.New(boundary + " != " + string(line))
    }
    delSize := len(boundary) + 4

    for {
        line, _, err = h.r.ReadLine()
        if err != nil {
            return err
        }
        if len(line) == 0 {
            break // 遇到空行,之后为请求体
        }
        delSize += len(line) + 2
        tmp := string(line)
        if index := strings.Index(tmp, ":"); index > 0 { // key: val
            h.header[strings.ToLower(strings.TrimSpace(tmp[:index]))] = strings.TrimSpace(tmp[index+1:])
        }
    }
    var filename string
    if tmp, ok := h.header["content-disposition"]; ok {
        for _, v := range strings.Split(tmp, ";") {
            if v = strings.TrimSpace(v); v == "form-data" {
                ok = false
            } else if strings.HasPrefix(v, "filename=") {
                filename = strings.Trim(v[9:], "\"")
            }
        }
        if ok { // 没有multipart/form-data则置空
            filename = ""
        }
    }
    size -= int64(delSize)

    fw, err := os.Create(filename)
    if err != nil {
        return err
    }
    defer fw.Close()
    pr := newProgress(h.r, filename, size)
    _, err = io.CopyN(fw, pr, size)
    pr.Close()
    if err != nil {
        return err
    }
    err = fw.Truncate(size - int64(len(boundary)) - 6)
    if err != nil {
        return err
    }
    h.respMessage("post ok")
    return nil
}

/* 下面是工具类 */
type progress struct {
    r    io.Reader
    cnt  int64
    rate chan int64
}

func newProgress(r io.Reader, file string, size int64) io.ReadCloser {
    cnt := 0
    for tmp := size; tmp > 0; tmp /= 10 {
        cnt++
    }
    // 之所以这样做进度,是因为打印耗性能,因此在协程中打印进度
    // 在处理数据中用非阻塞方式往chan中传处理字节数
    p := &progress{r: r, rate: make(chan int64)}
    go func(rate <-chan int64, format string, size int64) {
        for cur := range rate {
            fmt.Printf(format, cur)
        }
        fmt.Printf(format, size)
    }(p.rate, fmt.Sprintf("\r%s [%%%dd - %d]", file, cnt, size), size)
    return p
}

func (p *progress) Read(b []byte) (int, error) {
    n, err := p.r.Read(b)
    p.cnt += int64(n)
    select { // 非阻塞方式往chan中写数据
    case p.rate <- p.cnt:
    default:
    }
    return n, err
}

func (p *progress) Close() error {
    close(p.rate) // 关闭chan,通知打印协程退出
    return nil
}

var unitByte = []struct {
    byte float64
    unit string
}{
    {byte: 1},
    {1 << 10, "B"},
    {1 << 20, "KB"},
    {1 << 30, "MB"},
    {1 << 40, "GB"},
    {1 << 50, "TB"},
}

// 将字节数转为带单位字符串
func convertByte(b int64) string {
    for tmp, i := float64(b), 1; i < len(unitByte); i++ {
        if tmp < unitByte[i].byte {
            return fmt.Sprintf("%.2f %s", tmp/unitByte[i-1].byte, unitByte[i].unit)
        }
    }
    return strconv.FormatInt(b, 10) + " B"
}
