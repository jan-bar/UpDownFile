package main

import (
    "fmt"
    "io"
    "io/ioutil"
    "net/http"
    "net/url"
    "os"
    "path/filepath"
    "sort"
    "strconv"
)

var basePath string

func main() {
    switch len(os.Args) {
    case 2:
        basePath = "."
    case 3:
        basePath = os.Args[2]
    default:
        fmt.Printf("usage: %s ip:port [path]\n", os.Args[0])
        return
    }
    var err error
    basePath, err = filepath.Abs(basePath)
    if err != nil {
        fmt.Println(err)
        return
    }

    http.HandleFunc("/", upDownFile)
    fmt.Printf("handle [%s] [%s]\n", os.Args[1], basePath)
    err = http.ListenAndServe(os.Args[1], nil)
    if err != nil {
        fmt.Println(err)
        return
    }
}

func upDownFile(w http.ResponseWriter, r *http.Request) {
    if r.Method == http.MethodGet {
        err := handleGetFile(w, r)
        if err != nil {
            w.Header().Set("Content-Type", "text/html;charset=utf-8")
            w.Write(htmlMsgPrefix)
            w.Write([]byte(err.Error()))
            w.Write(htmlMsgSuffix)
        }
    } else if r.Method == http.MethodPost {
        err := handlePostFile(w, r)
        if err == nil {
            w.Write(respOk)
        } else {
            w.Write([]byte(err.Error()))
        }
    } else {
        w.Write([]byte(r.Method + " not support"))
    }
}

func handlePostFile(_ http.ResponseWriter, r *http.Request) error {
    fr, fh, err := r.FormFile("upload")
    if err != nil {
        return err
    }
    defer fr.Close()

    path := filepath.Join(basePath, r.URL.Path, fh.Filename)
    fw, err := os.Create(path)
    if err != nil {
        return err
    }
    pr := newProgress(fr, r.Method+": "+path, fh.Size)
    _, err = io.Copy(fw, pr)
    fw.Close() // 刷新到文件
    pr.Close()
    return err
}

func handleGetFile(w http.ResponseWriter, r *http.Request) error {
    path := filepath.Join(basePath, r.URL.Path)
    fi, err := os.Stat(path)
    if err != nil {
        return err
    }

    if fi.IsDir() {
        tmpInt, _ := strconv.Atoi(r.FormValue("sort"))
        if tmpInt < 0 || tmpInt > 5 {
            tmpInt = 0
        }
        dir, err := sortDir(path, sortDirType(tmpInt))
        if err != nil {
            return err
        }
        tmpInt = htmlIndex[tmpInt]
        w.Write(htmlPrefix[:tmpInt])
        w.Write(htmlChecked) // 加入默认被选中
        w.Write(htmlPrefix[tmpInt:])
        var link string
        for i, v := range dir {
            w.Write(htmlTrTd)
            w.Write([]byte(strconv.Itoa(i + 1)))
            w.Write(htmlTdTd)
            link = url.PathEscape(v.Name())
            if v.IsDir() {
                w.Write(htmlDir)
                link += "/"
            } else {
                w.Write(htmlFile)
            }
            w.Write(htmlTdTd)
            w.Write([]byte(convertByte(v.Size())))
            w.Write(htmlTdTd)
            w.Write([]byte(v.ModTime().Format(timeLayout)))
            w.Write(htmlTdTdA)
            w.Write([]byte(link))
            w.Write(htmlGt)
            w.Write([]byte(v.Name()))
            w.Write(htmlAtdTr)
        }
        w.Write(htmlSuffix)
        return nil
    }

    fr, err := os.Open(path)
    if err != nil {
        return err
    }
    defer fr.Close()

    size := fi.Size()
    wh := w.Header()
    wh.Set("Content-Type", "application/octet-stream")
    wh.Set("Content-Length", strconv.FormatInt(size, 10))
    wh.Set("Content-Disposition", "attachment;filename="+filepath.Base(r.URL.Path))
    wh.Set("Content-Transfer-Encoding", "binary")
    pr := newProgress(fr, r.Method+": "+path, size)
    _, err = io.Copy(w, pr)
    pr.Close()
    return err
}

const timeLayout = "2006-01-02 15:04:05"

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
    htmlIndex   = [6]int{172, 252, 340, 420, 508, 588} // 插入checked位置
    htmlPrefix  = []byte(`<html lang="zh"><head><title>list dir</title></head><body><div style="position:fixed;bottom:20px;right:10px">
<p><label><input type="radio" name="sort" onclick="sortDir(0)">目录升序</label><label><input type="radio" name="sort" onclick="sortDir(1)">目录降序</label></p>
<p><label><input type="radio" name="sort" onclick="sortDir(2)">时间升序</label><label><input type="radio" name="sort" onclick="sortDir(3)">时间降序</label></p>
<p><label><input type="radio" name="sort" onclick="sortDir(4)">大小升序</label><label><input type="radio" name="sort" onclick="sortDir(5)">大小降序</label></p>
<p><input type="file" id="upload"></p><progress value="0" id="progress"></progress><p><input type="button" onclick="uploadFile()" value="上传文件"></p><input type="button" onclick="backSuper()" value="返回上级"/>
<a href="#top">顶部</a><a href="#bottom">底部</a></div><table border="1" align="center"><tr><th>序号</th><th>类型</th><th>大小</th><th>修改时间</th><th>链接</th></tr>`)
    htmlSuffix = []byte(`</table><a name="bottom"></a><script>
    function uploadFile() {
        let upload = document.getElementById('upload').files[0];
        if (!upload) {
            alert('请选择上传文件');
            return
        }
        let params = new FormData();
        params.append('upload', upload);
        let xhr = new XMLHttpRequest();
        xhr.onerror = function () {
            alert('请求失败');
        }
        xhr.onreadystatechange = function () {
            if (xhr.readyState === 4) {
                if (xhr.status === 200) {
                    if (xhr.responseText === "ok") {
                        window.location.reload();
                    } else {
                        alert(xhr.responseText);
                    }
                } else {
                    alert(xhr.status);
                }
            }
        }
        let progress = document.getElementById('progress');
        xhr.upload.onprogress = function (e) {
            progress.value = e.loaded;
            progress.max = e.total;
        }
        xhr.open('POST', window.location.pathname, true);
        xhr.send(params);
    }

    function sortDir(type) {
        window.location.href = window.location.origin + window.location.pathname + '?sort=' + type;
    }

    function backSuper() {
        let url = window.location.pathname;
        window.location.href = window.location.origin + url.substring(0, url.lastIndexOf('/'));
    }
</script></body></html>`)
)

/*--------------------------------下面是工具类---------------------------------*/
const (
    sortDirTypeByNameAsc  sortDirType = iota // 目录升序
    sortDirTypeByNameDesc                    // 目录降序
    sortDirTypeByTimeAsc                     // 时间升序
    sortDirTypeByTimeDesc                    // 时间降序
    sortDirTypeBySizeAsc                     // 大小升序
    sortDirTypeBySizeDesc                    // 大小降序
)

type (
    sortDirType int
    dirInfoSort struct {
        fi       []os.FileInfo
        sortType sortDirType
    }
)

func sortDir(dir string, sortType sortDirType) ([]os.FileInfo, error) {
    fi, err := ioutil.ReadDir(dir)
    if err != nil {
        return nil, err
    }
    sort.Sort(&dirInfoSort{fi: fi, sortType: sortType})
    return fi, nil
}

func (d *dirInfoSort) Len() int {
    return len(d.fi)
}
func (d *dirInfoSort) Default(x, y int) bool {
    // 当其他选项比较相等时,使用文件名升序排序
    lx, ly := len(d.fi[x].Name()), len(d.fi[y].Name())
    if lx == ly {
        return d.fi[x].Name() < d.fi[y].Name()
    }
    return lx < ly
}
func (d *dirInfoSort) Less(x, y int) bool {
    if d.fi[x].IsDir() != d.fi[y].IsDir() {
        return d.fi[x].IsDir() // 目录永远在前面
    }
    switch d.sortType {
    default:
        fallthrough // 不在范围内采取目录升序
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
    }
}
func (d *dirInfoSort) Swap(x, y int) {
    d.fi[x], d.fi[y] = d.fi[y], d.fi[x]
}

/* 读IO加进度 */
type progress struct {
    r    io.Reader
    cnt  int64
    rate chan int64
}

func newProgress(r io.Reader, prefix string, size int64) io.ReadCloser {
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
        fmt.Printf(format+"\r\n", size)
    }(p.rate, fmt.Sprintf("\r%s [%%%dd - %d]", prefix, cnt, size), size)
    return p
}

func (p *progress) Read(b []byte) (n int, err error) {
    n, err = p.r.Read(b)
    p.cnt += int64(n)
    select { // 非阻塞方式往chan中写数据
    case p.rate <- p.cnt:
    default:
    }
    return
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
    tmp, unit := float64(b), "B"
    for i := 1; i < len(unitByte); i++ {
        if tmp < unitByte[i].byte {
            tmp /= unitByte[i-1].byte
            unit = unitByte[i].unit
            break
        }
    }
    return strconv.FormatFloat(tmp, 'f', 2, 64) + unit
}
