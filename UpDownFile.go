package main

import (
    "bufio"
    "encoding/base64"
    "errors"
    "fmt"
    "io"
    "net"
    "net/url"
    "os"
    "path/filepath"
    "strconv"
    "strings"
    "unsafe"
)

var authStr string // 授权信息

func main() {
    var addrStr, user, pass string
    switch len(os.Args) {
    case 2: // 无认证模式
        addrStr = os.Args[1]
    case 4: // 添加用户名密码认证
        addrStr, user, pass = os.Args[1], os.Args[2], os.Args[3]
        authStr = "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pass))
    default:
        fmt.Printf("usage: %s ip:port [user] [pass]\n", os.Args[0])
        return
    }

    addr, err := net.ResolveTCPAddr("tcp", addrStr)
    if err != nil {
        panic(err)
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
            err := handleFile(l)
            if err != nil {
                respData(l, err.Error())
            }
            l.Close()
        }(ln)
    }
}

const (
    maxMemory = 10 << 20 // 缓存10MB
    respMsg   = "HTTP/1.1 200 OK\r\nContent-Type:text/plain;charset=utf-8\r\nContent-Disposition:attachment;filename=resp.txt\r\nContent-Length:%d\r\n\r\n%s"
    getHeader = "HTTP/1.1 200 OK\r\nContent-Type:application/octet-stream\r\nContent-Disposition:attachment;filename=%s\r\nContent-Length:%d\r\nContent-Transfer-Encoding:binary\r\n\r\n"
)

func respData(w io.Writer, data string) {
    msg := data + "\r\n"
    fmt.Fprintf(w, respMsg, len(msg), msg)
}

func handleFile(l *net.TCPConn) error {
    br := bufio.NewReaderSize(l, maxMemory)
    method, path, length, err := getHeaderMsg(br)
    if err != nil {
        return err
    }
    fmt.Printf("[%s - %s - %d]\n", method, path, length)

    if method == "GET" {
        return httpGetFile(path, l, length)
    }
    err = httpPostFile(path, br, length)
    if err != nil {
        return err
    }
    respData(l, "post ok")
    return nil
}

// 内存复用,更快速,省内存
func bytesToString(b []byte) string {
    return *(*string)(unsafe.Pointer(&b))
}

func getHeaderMsg(r *bufio.Reader) (string, string, int64, error) {
    // 读取第一行,提取有用信息
    line, _, err := r.ReadLine()
    if err != nil {
        return "", "", 0, err
    }
    header := strings.Fields(bytesToString(line))
    if len(header) < 3 { // 首行至少3列数据
        return "", "", 0, errors.New("header error")
    }
    method, path := header[0], ""

    s := strings.Index(header[1], "?")
    if s >= 0 {
        path, _ = url.QueryUnescape(header[1][s+1:])
    }
    if path == "" { // ?号后面就是文件路径,需要解码url一下
        return "", "", 0, errors.New("path error")
    }

    var length int64
    if method == "GET" {
        fi, err := os.Stat(path)
        if err != nil {
            return "", "", 0, err
        }
        length = fi.Size() // GET请求提前得到文件大小
    } else if method != "POST" {
        return "", "", 0, errors.New(method + " not support")
    }

    var authCheck string
    for {
        line, _, err = r.ReadLine()
        if err != nil {
            return "", "", 0, err
        }
        if len(line) == 0 {
            break // 遇到空行,之后为请求体
        }
        header = strings.Split(bytesToString(line), ":")
        if len(header) == 2 { // 头部[key: val]解析
            header[0] = strings.ToLower(strings.TrimSpace(header[0]))
            header[1] = strings.TrimSpace(header[1])
            if method == "POST" && header[0] == "content-length" {
                length, _ = strconv.ParseInt(header[1], 10, 64)
            } else if header[0] == "authorization" {
                authCheck = header[1]
            }
        }
    }
    if authStr != "" && authStr != authCheck {
        return "", "", 0, errors.New("authorization error")
    }
    return method, path, length, nil
}

func httpPostFile(path string, r io.Reader, length int64) error {
    fw, err := os.Create(path)
    if err != nil {
        return err
    }
    defer fw.Close()
    pr := newProgress(r, length)
    _, err = io.CopyN(fw, pr, length)
    pr.Close()
    return err
}

func httpGetFile(path string, w io.Writer, size int64) error {
    fr, err := os.Open(path)
    if err != nil {
        return err
    }
    defer fr.Close()
    fmt.Fprintf(w, getHeader, filepath.Base(path), size)
    pr := newProgress(fr, size)
    _, err = io.Copy(w, pr)
    pr.Close()
    return err
}

type progress struct {
    r    io.Reader
    cnt  int64
    rate chan int64
}

func newProgress(r io.Reader, size int64) io.ReadCloser {
    p := &progress{r: r, rate: make(chan int64)}
    // 之所以这样做进度,是因为打印耗性能,因此在协程中打印进度
    // 在处理数据中用非阻塞方式往chan中传处理字节数
    go func(rate <-chan int64, all int64) {
        for cur := range rate {
            fmt.Printf("\rhandle:%4d%%", cur*100/all)
        }
        fmt.Printf("\rhandle: 100%%\r\n\r\n")
    }(p.rate, size)
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
