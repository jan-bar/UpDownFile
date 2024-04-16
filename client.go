package main

import (
	"compress/gzip"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/vbauerster/mpb/v8"
)

func clientMain(exe string, args []string) error {
	client := &fileClient{pBar: newMpbProgress()}

	fs := flag.NewFlagSet(exe+" cli", flag.ExitOnError)
	fs.StringVar(&client.data, "d", "", "<raw string> or @tmp.txt")
	fs.StringVar(&client.output, "o", "", "output")
	fs.BoolVar(&client.point, "c", false, "resumed transfer offset")
	caCert := fs.String("ca", "", "ca.crt to verify peer against")
	insecure := fs.Bool("k", false, "allow insecure server connections")
	timeout := fs.Duration("t", time.Minute, "client timeout")
	fs.BoolVar(&client.gzipOn, "g", false, "gzip file to send")
	auth := fs.String("auth", "", "username:password")
	err := fs.Parse(args)
	if err != nil {
		return err
	}

	if *auth != "" {
		var ok bool
		client.user, client.pass, ok = strings.Cut(*auth, ":")
		if !ok {
			return errors.New("invalid auth header")
		}
	}

	if client.httpUrl = fs.Arg(0); client.httpUrl == "" {
		return errors.New("url is null")
	}

	// 最小时间和http.DefaultTransport保持一致
	limitTime := func(d, limit time.Duration) time.Duration {
		if d < limit {
			return limit
		}
		return d
	}
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   limitTime(*timeout/2, 30*time.Second),
			KeepAlive: limitTime(*timeout/2, 30*time.Second),
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       limitTime(*timeout*2, 90*time.Second),
		TLSHandshakeTimeout:   limitTime(*timeout/6, 10*time.Second),
		ExpectContinueTimeout: limitTime(*timeout/60, time.Second),
	}

	// 证书配置,可以忽略证书,也可以携带ca.crt证书
	if *insecure || *caCert != "" {
		var root *x509.CertPool
		if *caCert != "" {
			pemCerts, err := os.ReadFile(*caCert)
			if err != nil {
				return err
			}

			root = x509.NewCertPool()
			root.AppendCertsFromPEM(pemCerts)
		}

		transport.TLSClientConfig = &tls.Config{
			InsecureSkipVerify: *insecure,
			RootCAs:            root,
		}
	}
	client.client = &http.Client{Timeout: *timeout, Transport: transport}

	pool := bytePool.Get().(*poolByte)
	defer bytePool.Put(pool)
	client.buf = pool.buf

	if client.data != "" {
		err = client.post()
	} else {
		err = client.get()
	}
	if err == nil {
		client.pBar.Wait()
	}
	return err
}

type fileClient struct {
	client  *http.Client
	pBar    *mpb.Progress
	httpUrl string
	data    string
	output  string
	user    string
	pass    string
	buf     []byte
	point   bool
	gzipOn  bool
}

func (fc *fileClient) getServerFileSize(url string) (int64, error) {
	req, err := fc.newRequest(http.MethodGet, url, nil, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set(headerType, typeOffset)

	resp, err := fc.client.Do(req)
	if err != nil {
		return 0, err
	}
	//goland:noinspection GoUnhandledErrorResult
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		if off := resp.Header.Get(offsetLength); off != "" {
			return parseInt64(off)
		}
		return 0, nil // 没有长度,回退到全量上传
	case http.StatusNotFound:
		return 0, nil // 服务器没有文件
	default:
		info, _ := io.ReadAll(resp.Body) // 其他错误
		return 0, fmt.Errorf("code:%d,resp:%s", resp.StatusCode, info)
	}
}

func (fc *fileClient) newRequest(method, url string, body io.Reader, hd http.Header) (*http.Request, error) {
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return nil, err
	}

	if hd != nil {
		req.Header = hd
	}

	if fc.user != "" && fc.pass != "" {
		req.SetBasicAuth(fc.user, fc.pass)
	}
	return req, nil
}

func (fc *fileClient) post() error {
	var (
		size int64
		body io.Reader
		hd   = make(http.Header)
	)
	if path, ok := strings.CutPrefix(fc.data, "@"); ok {
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

		// 设置读取文件的进度条,进度条长度为文件大小
		pr := &progressBar{r: fr, b: newMpbBar(fc.pBar, http.MethodPost, path, size)}
		defer pr.Close()

		if fc.gzipOn {
			ir, iw := io.Pipe()
			go func() {
				gw, _ := gzip.NewWriterLevel(iw, gzip.BestCompression)
				_, ie := io.Copy(gw, pr)
				_ = gw.Close() // 使用gzip压缩数据推送到服务器,传递错误
				_ = iw.CloseWithError(ie)
			}()
			hd.Set(headerType, typeGzip)
			hd.Set(offsetLength, strconv.FormatInt(size, 10))
			body, size = ir, 0 // 强制服务器使用自定义长度解析数据
		} else {
			hd.Set(headerType, typeDefault)
			if fc.point { // 获取服务器文件大小,作为客户端上传的断点
				cur, err := fc.getServerFileSize(fc.httpUrl)
				if err != nil {
					return err
				}

				if cur > 0 {
					if cur >= size {
						return errors.New("file upload is complete")
					}
					if bs, ok := pr.r.(io.Seeker); ok {
						_, err = bs.Seek(cur, io.SeekStart)
						if err != nil {
							return err
						}
						pr.b.SetCurrent(cur)
						size -= cur // 设置当前长度,并设置实际发送数据长度
						hd.Set(offsetLength, offsetAppend)
					}
				}
			}
			body = pr
		}
	} else {
		sr := strings.NewReader(fc.data)
		size, body = sr.Size(), sr // 推送字符串到服务器
	}

	req, err := fc.newRequest(http.MethodPost, fc.httpUrl, body, hd)
	if err != nil {
		return err
	}
	req.ContentLength = size // req.Header.Set(headerLength, "size")

	resp, err := fc.client.Do(req)
	if err != nil {
		return err
	}
	if resp.Body != nil {
		if resp.StatusCode != http.StatusOK {
			_, _ = io.CopyBuffer(os.Stdout, resp.Body, fc.buf)
		} else {
			_, _ = io.CopyBuffer(io.Discard, resp.Body, fc.buf)
		}
		_ = resp.Body.Close()
	}
	return nil
}

func (fc *fileClient) get() error {
	req, err := fc.newRequest(http.MethodGet, fc.httpUrl, nil, nil)
	if err != nil {
		return err
	}

	var out io.Writer
	if fc.output == "-" {
		out = os.Stdout
	} else {
		if fc.output == "" {
			fc.output = filepath.Base(req.URL.Path)
		}

		fileFlag := flagW
		if fi, err := os.Stat(fc.output); err == nil {
			if fi.IsDir() {
				return fmt.Errorf("%s is dir", fc.output)
			}

			if !fc.gzipOn && fc.point {
				fileFlag = flagA // 断点续传,服务器识别该Header,从文件指定偏移返回
				req.Header.Set(headerRange, fmt.Sprintf("bytes=%d-", fi.Size()))
			}
		}
		fw, err := os.OpenFile(fc.output, fileFlag, fileMode)
		if err != nil {
			return err
		}
		//goland:noinspection GoUnhandledErrorResult
		defer fw.Close()
		out = fw
	}

	if fc.gzipOn {
		req.Header.Set(headerType, typeGzip)
	}

	resp, err := fc.client.Do(req)
	if err != nil {
		return err
	}
	if resp.Body == nil {
		return errors.New("body is null")
	}
	//goland:noinspection GoUnhandledErrorResult
	defer resp.Body.Close()

	var size, cur int64
	switch resp.StatusCode {
	case http.StatusPartialContent:
		cur, _, size, err = scanRangeSize(resp.Header)
		if err == nil {
			break // 服务器返回分片,客户端进度条正常显示
		}
		fallthrough
	case http.StatusOK:
		size, err = parseInt64(resp.Header.Get(headerLength))
		if err != nil { // 正常返回长度解析失败,用自定义长度
			size, _ = parseInt64(resp.Header.Get(offsetLength))
		}
	case http.StatusRequestedRangeNotSatisfiable:
		size, _ = io.CopyBuffer(io.Discard, resp.Body, fc.buf)
		fmt.Printf("already downloaded [%d bytes data]\n", size)
		return nil
	default:
		_, _ = io.CopyBuffer(os.Stdout, resp.Body, fc.buf)
		return nil
	}

	if size == 0 {
		return errors.New("size == 0")
	}

	rb := resp.Body
	if fc.gzipOn {
		rb, err = gzip.NewReader(resp.Body)
		if err != nil {
			return err
		}
		//goland:noinspection GoUnhandledErrorResult
		defer rb.Close()
	}

	if out != os.Stdout {
		pw := &progressBar{w: out, b: newMpbBar(fc.pBar, http.MethodGet, fc.output, size)}
		pw.b.SetCurrent(cur)
		defer pw.Close()
		out = pw
	}

	_, err = io.CopyBuffer(out, rb, fc.buf)
	return err
}

func scanRangeSize(h http.Header) (first, last, length int64, err error) {
	var n int // Content-Range: bytes (unit first byte pos) - [last byte pos]/[entity length]
	n, err = fmt.Sscanf(h.Get("Content-Range"), "bytes %d-%d/%d", &first, &last, &length)
	if n != 3 {
		err = fmt.Errorf("scanRangeSize n=%d", n)
	}
	return
}
func parseInt64(s string) (int64, error) {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, err // 强制返回0
	}
	return n, nil
}
