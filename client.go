package main

import (
	"compress/gzip"
	"crypto/tls"
	"crypto/x509"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/pkg/errors"
)

func clientMain(exe string, args []string) error {
	var client fileClient
	fs := flag.NewFlagSet(exe+" cli", flag.ExitOnError)
	fs.StringVar(&client.data, "d", "", "<raw string> or @tmp.txt")
	fs.StringVar(&client.output, "o", "", "output")
	fs.BoolVar(&client.point, "c", false, "resumed transfer offset")
	caCert := fs.String("ca", "", "ca.crt to verify peer against")
	insecure := fs.Bool("k", false, "allow insecure server connections")
	timeout := fs.Duration("t", time.Minute, "client timeout")
	fs.BoolVar(&client.gzipOn, "g", false, "gzip file to send")
	err := fs.Parse(args)
	if err != nil {
		return err
	}

	if client.httpUrl = fs.Arg(0); client.httpUrl == "" {
		return errors.New("url is null")
	}

	client.client = &http.Client{Timeout: *timeout}
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

		client.client.Transport = &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: *insecure,
				RootCAs:            root,
			},
		}
	}

	pool := bytePool.Get().(*poolByte)
	defer bytePool.Put(pool)
	client.buf = pool.buf

	if client.data != "" {
		return client.post()
	}
	return client.get()
}

type fileClient struct {
	httpUrl string
	data    string
	output  string
	point   bool
	gzipOn  bool
	client  *http.Client
	buf     []byte
}

func (fc *fileClient) getServerFileSize(url string) (int64, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set(headerType, typeOffset)

	resp, err := fc.client.Do(req)
	if err != nil {
		return 0, err
	}
	_ = resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return 0, nil // 服务器没有文件
	}
	return parseInt64(resp.Header.Get(offsetLength))
}

//goland:noinspection GoUnhandledErrorResult
func (fc *fileClient) post() error {
	var (
		size int64
		path string
		ok   bool
		body io.Reader
	)
	if path, ok = strings.CutPrefix(fc.data, "@"); ok {
		fr, err := os.Open(path)
		if err != nil {
			return err
		}
		defer fr.Close()

		if fc.gzipOn {
			pr, pw := io.Pipe()
			go func() {
				gw, _ := gzip.NewWriterLevel(pw, gzip.BestCompression)
				io.Copy(gw, fr)
				gw.Close()
				pw.Close()
			}()
			body = pr
		} else {
			fi, err := fr.Stat()
			if err != nil {
				return err
			}
			body, size = fr, fi.Size()
		}
	} else {
		sr := strings.NewReader(fc.data) // 不是文件,则上传一段文本内容
		path, size, body = "<string data>", sr.Size(), sr
	}

	header := make(http.Header)
	if fc.gzipOn {
		header.Set(headerType, typeGzip)
	} else {
		header.Set(headerType, typeDefault)
		if fc.point {
			// 获取服务器文件大小,作为客户端断点偏移
			cur, err := fc.getServerFileSize(fc.httpUrl)
			if err != nil {
				return err
			}

			if cur > 0 {
				if cur >= size {
					return errors.New("file upload is complete")
				}
				if bs, ok := body.(io.Seeker); ok {
					// 客户端文件跳转到服务器文件末尾的偏移
					_, err = bs.Seek(cur, io.SeekStart)
					if err != nil {
						return errors.WithStack(err)
					}
				}
			}
			header.Set(offsetLength, string(strconv.AppendInt(fc.buf[:0], cur, 10)))
		}
	}

	req, err := http.NewRequest(http.MethodPost, fc.httpUrl, body)
	if err != nil {
		return err
	}
	req.Header = header
	req.ContentLength = size // req.Header.Set(headerLength, "xxx")

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
		resp.Body.Close()
	}
	return nil
}

//goland:noinspection GoUnhandledErrorResult
func (fc *fileClient) get() error {
	req, err := http.NewRequest(http.MethodGet, fc.httpUrl, nil)
	if err != nil {
		return err
	}
	if fc.output == "" {
		fc.output = filepath.Base(req.URL.Path)
	}

	fileFlag := os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	fi, err := os.Stat(fc.output)
	if err == nil {
		if fi.IsDir() {
			return errors.Errorf("%s is dir", fc.output)
		}

		if !fc.gzipOn && fc.point { // gzip压缩时不能进行断点续传
			fileFlag = os.O_CREATE | os.O_APPEND
			sSize := string(strconv.AppendInt(fc.buf[:0], fi.Size(), 10))
			// 断点续传,服务器识别该Header,从文件指定偏移返回
			req.Header.Set("Range", "bytes="+sSize+"-")
		}
	}
	fw, err := os.OpenFile(fc.output, fileFlag, fileMode)
	if err != nil {
		return err
	}
	defer fw.Close()

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
	defer resp.Body.Close()

	var size, cur int64
	switch resp.StatusCode {
	case http.StatusOK:
		// 刚开始下载,返回文件完整大小
		size, err = parseInt64(resp.Header.Get(headerLength))
		if !fc.gzipOn && err != nil {
			return err
		}
	case http.StatusPartialContent:
		// 获取断点位置,服务器从断点返回数据
		cur, _, size, err = scanRangeSize(resp.Header)
		if err != nil {
			return err
		}
	case http.StatusRequestedRangeNotSatisfiable:
		// 已经下载完毕,无需重复下载
		size, _ = io.CopyBuffer(io.Discard, resp.Body, fc.buf)
		fmt.Printf("already downloaded [%d bytes data]\n", size)
		return nil
	default:
		_, _ = io.CopyBuffer(os.Stdout, resp.Body, fc.buf)
		return nil // 打印错误
	}
	fmt.Println(size, cur)

	rb := resp.Body
	if fc.gzipOn {
		rb, err = gzip.NewReader(resp.Body)
		if err != nil {
			return err
		}
		defer rb.Close()
	}

	_, err = io.CopyBuffer(fw, rb, fc.buf)
	return err
}

func scanRangeSize(h http.Header) (first, last, length int64, err error) {
	var n int // Content-Range: bytes (unit first byte pos) - [last byte pos]/[entity length]
	n, err = fmt.Sscanf(h.Get("Content-Range"), "bytes %d-%d/%d", &first, &last, &length)
	if n != 3 {
		err = errors.Errorf("scanRangeSize n=%d", n)
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
