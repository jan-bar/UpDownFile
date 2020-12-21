package main

import (
    "bufio"
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

func main() {
    if len(os.Args) != 2 {
        fmt.Printf(`usage: %s ip:port

get file:
  wget --content-disposition "http://ip:port?/root/tmp.txt"
  curl -OJ "http://ip:port?/root/tmp.txt"
post file:
  wget -q -O - --post-file=d:\tmp.txt "http://ip:port?/root/tmp.txt"
  curl "http://ip:port?/root/tmp.txt" --data-binary @d:\tmp.txt
`, os.Args[0])
        return
    }
    addr, err := net.ResolveTCPAddr("tcp", os.Args[1])
    if err != nil {
        panic(err)
    }
    ser, err := net.ListenTCP("tcp", addr)
    if err != nil {
        panic(err)
    }

    fmt.Printf("Listen: [%s]\n", addr)
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

func getHeaderMsg(r *bufio.Reader) (string, string, int64, error) {
    // 内存复用,更快速,省内存
    bytesToString := func(b []byte) string {
        return *(*string)(unsafe.Pointer(&b))
    }

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

    for {
        line, _, err = r.ReadLine()
        if err != nil {
            return "", "", 0, err
        }
        if len(line) == 0 {
            break // 遇到空行,则之后为请求体
        }
        if method == "POST" { // POST请求才需要通过header找到消息体长度
            header = strings.Split(bytesToString(line), ":")
            if len(header) == 2 && strings.ToLower(header[0]) == "content-length" {
                // 获取消息体长度字节数
                length, _ = strconv.ParseInt(strings.TrimSpace(header[1]), 10, 64)
            }
        }
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
            fmt.Printf("\rhandle:%4d", cur*100/all)
        }
        fmt.Printf("\rhandle: 100\r\n\r\n")
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
