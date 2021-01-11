package main

import (
    "bytes"
    "compress/zlib"
    "crypto/cipher"
    "encoding/base64"
    "errors"
    "flag"
    "fmt"
    "io"
    "io/ioutil"
    "net"
    "net/http"
    "net/url"
    "os"
    "path/filepath"
    "sort"
    "strconv"
    "strings"
    "sync"
    "time"
)

const (
    timeLayout   = "2006-01-02 15:04:05"
    encryptFlag  = "encrypt"
    headerLength = "Content-Length"
    janbarLength = "Janbar-Length"
    headerType   = "Content-Type"
    urlencoded   = "application/x-www-form-urlencoded"
)

func main() {
    if len(os.Args) >= 2 && os.Args[1] == "cli" {
        if err := clientMain(); err != nil {
            fmt.Println(err)
        }
        return
    }

    var addrStr string
    flag.StringVar(&basePath, "p", ".", "path")
    flag.StringVar(&addrStr, "s", ":8080", "ip:port")
    flag.BoolVar(&useEncrypt, "e", false, "encrypt data")
    flag.Parse()

    addr, err := net.Listen("tcp", addrStr)
    if err != nil {
        fmt.Println(err)
        return
    }
    addrStr = addr.Addr().String()

    basePath, err = filepath.Abs(basePath)
    if err != nil {
        fmt.Println(err)
        return
    }

    fmt.Printf(`dir [%s],url [http://%s/]

GET file:
    wget -c --content-disposition "http://%s/dir/tmp.txt"
    curl -C - -OJ "http://%s/dir/tmp.txt"
POST file:
    wget -qO - --post-file=C:\tmp.txt "http://%s/dir/tmp.txt"
    curl --data-binary @C:\tmp.txt "http://%s/dir/tmp.txt"
    curl -F "file=@C:\tmp.txt" "http://%s/dir/"
`, basePath, addrStr, addrStr, addrStr, addrStr, addrStr, addrStr)

    http.HandleFunc("/", upDownFile)
    http.HandleFunc("/favicon.ico", faviconIco)
    err = (&http.Server{ReadHeaderTimeout: time.Second * 30}).Serve(addr)
    if err != nil {
        fmt.Println(err)
    }
}

var (
    bytePool = sync.Pool{New: func() interface{} {
        return make([]byte, 32768) // 32<<10
    }}
    icoData struct {
        sync.Once
        data []byte
    }
    basePath   string
    useEncrypt bool
)

func faviconIco(w http.ResponseWriter, _ *http.Request) {
    icoData.Do(func() {
        const ico = "eNrsnAtYVdeVgDc+Yh5NNEmbzqRJTJOZaZJvpk2/zqTTpm0mmU5nvmmbr5Pm0ZlM00liuAu9GxFBRBRR4/uBr/hEERUfCAcUX3BERVF8AJIoEU2C+EbBywV5XLxw13xr33XweHKBCwJCPvf3bdnee+45e/9n7bXWXvshRIAIEG++Tn+fFi8OFeIxIcRzQog3hRBxwvs5pf8LFOKRB7z5TiQAac33AshvA8jnAOSfAeQcAJkDIL8AkNhKLgGQOoCcCyDfApAv8H3utT7jm5gsbQwAkAMB5I8A5FAAmQEgKwFkg81mv2Gz2ZsCA4d5AgOHoc1mR7s9BKUMRbt9hGJJn/P3jXx9A/9+O4AcBiCfB5CD+DnfKK4+5PERAPkKy9V54kVsPvpomGIWERGF0dETcdKkqTht2iyMjV2Aq1cn4vr1SbhmzTqcN2+h+nzy5OkYEzMZR48eiyEhYWjch/7Sffn+/8Yy+42QV0s77geQvwKQ8wHkJWr3Rx8NVQzHjZuAs2fPw6SkFDx06AiWlJzBykontpbq6urw0qXLePRoPqanb8fFi5fhhAmTMSQkXN2XuZYDyGUA8l8A5IDezNTC8hkAGQEgT1N/NTiSnGnaZjx+vEjxuZ3U1NSEX31Vgjt2ZODcuQswLCwSSeaZ6ykAGQYgn+6NTE117sOysRFA3iC9N2xYCE6fPlvJ1IULF7ErktPpxH37cnDRoqVKF9D7A5BNADIJQP4GQPbrLUwtNucvADKfZJLkhHQdyeO5c+exO5LD4cDMzF0YE/Oxej7L6nEAGdQbmJpYPgggw9kuKJtMMpmXV4Aulwu7MzU2NuKpU6dx5coE5R8w0zIAOR5APtxTmVpYxgBIJ3EcOnQ4JiSsxdLSs3gn07Vr1zAlJRVHjBhl+K81AHJWT2RqYTmefUHFcsOGTVhRcQ17Qqquvq70amhohMH0ek9katKXHwDIi1TXoKBgxdLhqMSelBoaGjAn56CZKflUIQCy753mafGJfgcgi2+yTOpxLI3kcnmZmvr+GQD533falzI9/+8B5E7D9iQmbuyxLM3jga1btyudxPXexePfO8LTxPJbAPJjwyeicc758xewNyTyp+LjV5tjLAt43N/tTE08f039hXz1qKjxWFj4KSJ6sLck8jumTJlh+FGk+/+ru3maWP4VgFxr2PL09G1448YN7E2JxqkHDuQqXcpMU83j0i6KCfnKZM//A0BWk2zGxi7AixcvYW9MVVVVykemdgDIegD5jjXW11r2kyP5D4M5Tvs85+fYxxQcB1tP7zQkJByzsvZgb06FhZ/h6NHjDKYagHyC2/kAgPyBicELLL8txq0tn9/HMYMojh/oADLTZrNTJhs+g+NuvzBkc+7chXj16tVezbO2tlbFWbnPVwHI1wDkTwHkZJvNvoPaTxyYRzKAnAQgXzePBQympv9/F0BOtNnsXxqxcdKNdnsIjhgRjsOHhxl28BiAXEffBwePxG3bdtxSN48H0d2I2HDDmxubegAwD43lb9bJ7fbW05zy8vKVjHK7l/C8ixr3Ux80fGtvzFHxOctx66d96AGyLYttNruLuM2ZM0/FZYgr/V23bgNu3LhJxc75ni76btKkqXjmTKmqDzGsqUN0VCGWOxCvXPPmikrEymrE+oavt6Hr7Q1iXT1iJdWp8madqH5UT6ovcfb6T5U4b94nOGSIiu85AGRTeHgkJiaux4SENYoj2a358z9R8wQkY4GBdjeATACQT1li56NsNntdaOgoXLVqNR48eBAXLVqCQ4YE4cyZsZiTk4P5+fm4ZMlyFcM0fLa4uHhlI+td3vpevIp4vgzxXJn3rypf9v4tq0Csun6z/l2dbri9zC6V31oPc/2ovvS+XQ3e36Rqm5vjUCQ3EydOwSNHjmBW1m7V3lGjojAtbQvm5ubi2rXrkHgHBqr5q/mmvv9Dkl2SyxUr4vHQoUP46aeFKh5LPGfMmIPZ2fvw6NGj6nviSc+j97N9Rwa6biBeLr9Z19byhSveNnY1U2JJnPypk/GuG9xeuzR2bIzql8ST+h/x0PVdzHMMaloaHjt2THEmuWVfy8k6tw+AXBgUJJsmT56GBw/mKjmk633xJHk0eI4bNwGPnzit+o+/9VZMyxCd17uu71Mfp/7dnjpRrnAinj9fpuZh2uJZUJCvONF3M2fOMXQg6dyBZH9CQsI8pCfomry8vDZ50vM+/ngGnrtY3e56U6Y+WN9FoeXaesSLV7BD9SJ9tGDhEjVH0hZP4lRQUIBJSck4cmQEydhJAPm4zWZvDAsbjTt3Zqhr/OU5ddpcv/u5r+ys7nwZJdkkfXKug3W6VoW4Zu0mtLONaIsnyR99Fxmp/IIaskvG3A7ZIH95ks6OW7lR6cOO1JvaS/bL3cl6lIa7pAs7+o6p3ySnbFfzeP7wpHz48GGMjp5APOtoDEQ8iS/x8p9nKK5cldJhns02oJOH+2SnDR+jI5nak70/T8mXvzxJRsePn0R9tsM8Q0JGoZa267Z4Kh3a0HY8va6uHmtr61SZfLPWEunkjtbH4LknOx8jIrqT53AcOXI0Zui5XSKfdXV1ePbseSwoKMSMjF2YlpaOKSlpqnzgQK6arywvr/DJtjPkc19OfjfLp5fnrt0d50m+dYVFfxKf06e/UHP0NCb78EPADz6wqfz++7bmMo39PvlkKe7Zk63m8j0mo0Z+Z3v9N2ufyTmQj5GR3c0zAnU9+7Z0f1XNrbJVUnJGjUfomcbYhLglJKzFxMQNuHx5vKoL+c/EmvKsWXPVXPAtvmf1rWOh9tjIa07EQ4fyu11/BgePxBRtM1bXtq8vUV+kel+uuDnGM9LJk8Vq3Rw9b8eODPV/h8OBbrebY0CkB86pPp+YuBEnTJiimF+9Wn6rvnAhXrp683ntqR/5rnl5BSou0p08pQxV8ZHGJsSrjrbl4dxlD35adAWzDxSrPlVd83Xf0+VqULrRn7UPZKNOnfoCT5489TU9Svcl2S88UYa5R0vwq3Outnmy/qEqZWXtUf2vO3nSvakvGjbAl84y69bSi25M356LsXOX4aWy69jk6fLwHO7JPqx8uuMnK5rrQn996fxyx03bmJycilKO6Faeanw0dSbW86CR/Ggal5jrujenCE+X1Kjy2UtNmL49R/3mypWKbokvbd68FefELsGi4nKvnrnswX0Hi7Hgs8uqbMSXnNdvtYukp434Ukd5kv6l37WHJ92DbIjZFpCsUl+uqPTg3HlLccEnqzD3SDHGJyTjmDETlF+wZs06/PLLr9r0JTs+D1yJup6l6jd8eDjOmDkfM/Vc3JlxECdOmolZe/KworJJ1ZNk0txXKiudysb5O36nfOTIERUXDgxsHh95wsMjMTNT95snvYOwsEjMzt7/9b7m8cbkyX8MHxWFo0dHq5hNSkoqbtmyVcWqx4wZr/ydxk4O3JG9ovqSnVq5MkH5rkuXxuHYsRNUfZctW4GVlVXq3fuKHRQVfc5shvnFk2QzK2s3RkZGE5Na5nluxIhwD9kXf+NLxhxxYuL6VttGcj937kIsK7ui1irSGKeykuRnt5KhmpraTuVJ/igxLC09h/X19crGkV9w4MBBHDMmGpOTtVbntLdt26F8F3/7O/EiOQkLG02/+RJAfg9ArgsKCm6cPn2Wkl1/edI79Pp/Dp91O3ToCC5cuFjN1XkswkA+ELXL08kBJtIhvu5Lz8vMzFJtIs4t+Qwky7ye2S+exCQ2dj4OHarin4m83+IPNpvdST7Chg1J6rrCwsK2eKq9FOHhY3D//gMtrGVtUvOGHk/PWC9CuoV0a0v1ob5uzJkBSFdQUHBTyzwLWDY19ZnNZjfm7fvzvEcc8YmKGo/p6VsVu8WLl/L80Rzcv3+/4rx8+Uri2QggLwHIOvrNihWrWnznvSUR482b0431YU0AMicoKLhq0qQpKh63a1dWM8+0tC2KxfbtOzAmhuy6mg9NB5CPm+bk/g5AHiLbRLqAdDn1f5J9ekepqZvV78nfGTp0ONmxlQByh7FuqaDgWK/meeZMqWoby+ZnAHICgCwj/U82NCkpWfEk/yQ+PkHNxZGPySw/BZA/t6wp6QMgXwKQKTabvdo810xjIerXo0ZFGZ/R834JIN8DkA1Uh/j41VhTU9MrWZK+Jdk02str/1/g9Qtq7S35BoYNDg4ORV5PWMvrHF8z1uP6yN/j+6UByIPcr5t4f8Ql/vxNvvYpALndq0cj1RrVnqIr25OKik6quUWWzUIA+SK3j2RmNfk/1G/JZgDIKwDyKPVNXkPzfT/W3BjrG37MazvL+b0V8Boms0y/BSAdpBfIxzxz5myvYulwOJRN5zUhbgA5yrI2iTgc4PZXAMiJvA7naX/WhfngSr/bx/erBJB/NO/dAZCP8Z4+1Qdo7FNdXd0rWNJYOT19m3l9RgaA/FvLGsKfAcjL/P0+bm+H9oOafhNu7NngNT2PWu5JzzxqrLXLzNyl/OienMgfzc09rOwCy+ZZ7mvW/SqpbO+p/fJ21oWa7vskgNzD+qOO9UY/y9rG9419rmS3yCftqWtryecnnTl2bIzBsobtuXUN4nsmOco19ip30trat3lfmbGX/2WLf/AQgJwOIGupjhERY1Xct6cxJZbkG02ePN1g6QKQy3304+cBZB6318HtF53I834AuR5A3mD532ld+8h6YK0xdiI57UlM3e5GFXuPjp5k+EYeXs+pbGzq2oFiePAQYw1tPLfVze1+sAvWf/8DgDxh2q83w9LvBevzrVwPpZ9In97p8VNDQ4N6t5GR0YbtIZnYS2363R/mByAKsTtN9Dusi3uGDQsKM/k0X7KsBnTmHgVm1Y99T+NZ5I/a2G8SlrFWEutatQ4vMXGDmjfrqphna+NIp9OJmzZpamzD9W4AkNnE8rvPFgfs3iz66ZoYnJUq3kha9diCGZNfvRAW+h7ahwGNa94b8lFo/9Hhf+qqPR33s+6u5roVAcjfWsYHARxnIT/qmrHGefbseSrmcLtnB/jvD9VjcfFpXLBgUfPZI1xv6r/PvPvn6ICM5L5C18SvdE0c0TXh1rUAd0ZKH8+Wdd/CVYv/JmfDyr/+0e7Noo+25uGu3CcziHV4A9fxMx9MjesiuM/w+DdCnQVSUnKmy7i6XC41p5ecnGr2hyhfAJAzAeR36J3rmiCWj+iamK9rAn3kJl0Tubom/pmv7Uqm5OensM42xmm+mPblzzMNmaZxnXG2QHHxqU7RA9SvHY5KtR5iy5ZtGBUVY4wfDRu+j88jaj5HZO3yp0R2uvi+rgVsa4GnwXSfrolnuoKphdWzvNfBbRqPvm09+8Q01o/h8xpcNpv3HJGwsNFqvJeVtQePHz+h4us0BmzLfpEMEr/z5y/giRNFau4kLi5e+RSm81hucOwnlve83FKnl1/b0H/qxF+/m7L60aJdLfOk7NI1MU/XlJ7t6n3FZM83AchGk286gs9Xsl5LduufAOQUAHnIy1Wd86PirKGhESp2HRe3Sp11k5mZpThZs65nqb68YkWCmh/wnr0yFE17Utz83maxn+wr9vOAzTZ82PDgISVL5v0Qs9Ja5Un5pCGj3bBX+zn216pM4/xFltiJOZOf8I8ce0jm82eMM5QUG+JrrKnxlel7C0Pj3KVUHr/RGPieFp5P/s9sw0+ZGP2fnq0b7nO3IaNluib+2FU8fTB9nGMvFab9uqQz/7eF/m/kJ/jcKRuftbaDZetCG+etka/2OT9jAYAMBZC/Z70e0Mp7fIvjHk3edxhcNTx4yLy0xIeWee17izzLdU38pSt5+mD6MI/ji03tPgcg46zn9LSQH2Q99xLvW6a2/4+P/Da/g5c57jvIj32WvwSQi1kfGXUrBZBBb74z9ZGcbeIJXROLdU1cb4HnGV0TLxo2qRu59ufYf4rJnyJZOMk64FWrPmvHHtz2ZtLXPwGQ09inM3S8m8+6e9Xbd4LF3i2K0WO6JoJ0TRTqmvBYeH6ua2KArokHdE18p5uZGv34A5bVJpPfQr7oBpa9h7lvdvjcPh97ngM4NvM6gFzDOqGWn+/hsy2GmuPB4yLfECa5u0/XxA9YV9p0TYzQNXFC18Q2/v4lXRMrdE384g4wHcBjz2kcm3Kb5OMq68kVrGOf5r24A1jG+7F8tZT7cb6H42xP8lg4lucjyky+cSPrnY9ZP9zr6/2Z+7Kuib4sjySz+bomVvHnD+maiNY1kaFr4qfdwdQH1wfYr5rI+qvOJLMNfFbTZearAchxbJ9+00Im3TkEQI5hv2IPn63g4P3raIrVnmL/7HkeK7fZDyxcv826c6SuCWNM9biuiQRdE0t0TTx6h5ga8kpy9CHHoM4zS5fJRjTx/+v4fKSWch1f52Z25ndzge3+Bzy/OKAjOoX5kQyeZdseYOL8hq6J/bomfttdPFvRcX25Tz/LY8CFHLstYRYVJllztZAr+bqLrBOPAcilfL/BrAf63s7Zn7om+uiaeF3XRImuiZ9Z5JY4H9M1EaVron938vTj3MA+3PZn+DxGkt9g9gd85cU8BqPr/pV1iaFvAzrr7A8eX45lnk8azJgn8f1M18Qc1qk9/RzbdufOTix3NG4v0jVxr0U+f69r4rKuidk9kWdPTGzfyU/K4rLB8h5dEzHsl05mn/Ruap2lwZN0ZBwzND7/d/bviWdgd9ujXsyTxkFf6ZoYb2L5qq6JXczyiK6Jn9zl6TfPH7OvFKtr4k+6Jmawb08snbompCG3d5NfvudbuiYqdE2UMtcmZlnBdv/Ru7LZLp52XRPVpphIg66Jnbom3tE1Meguy3bzXMRzHQ1s49/VNTHYmPe4y7LdY6NNHFt6T9dUfLR/d8U/v6E81+ua+LnF92wzIWKp94+T/okRQrziLXvo64HeciOVB3jLLir39ZadVA7wlkv5flTeq8rjVTlGlV9RZe8zB1vLjd7yAGvZ5S33tZad3nIAOoWzuSVOsbe5/KKphYPuCkiH0iARY+JZauLcaBQHmMoDncLjqzzYKdBcNm76iqW813RN6c373FJ2msouU7nRVDYe3Ncre+oRAV6ZHOjx1t+Q1b0ipFmeneLFZjlvVGVV68FcdpHkNQ5SZYwRryB/7t07ZCq7WiyHtFB23iwHmMp9TeWBTvH/AQAA///m2pYU"
        zr, _ := zlib.NewReader(base64.NewDecoder(base64.RawStdEncoding, strings.NewReader(ico)))
        data := bytes.NewBuffer(make([]byte, 0, 27206))
        io.Copy(data, zr)
        zr.Close()
        icoData.data = data.Bytes()
    })
    w.Write(icoData.data)
}

func upDownFile(w http.ResponseWriter, r *http.Request) {
    var err error
    buf := bytePool.Get().([]byte)
    defer bytePool.Put(buf)
    switch r.Method {
    case http.MethodHead:
        err = handleHeadFile(w, r, buf)
    case http.MethodGet:
        err = handleGetFile(w, r, buf)
    case http.MethodPost:
        err = handlePostFile(w, r, buf)
    default:
        err = errors.New(r.Method + " not support")
    }
    if err != nil {
        w.Header().Set(headerType, "text/html;charset=utf-8")
        w.Write(htmlMsgPrefix)
        w.Write([]byte(err.Error()))
        w.Write(htmlMsgSuffix)
    }
}

func handlePostFile(w http.ResponseWriter, r *http.Request, buf []byte) error {
    var (
        path string
        size int64
        fr   io.ReadCloser
    )
    if r.Header.Get(headerType) == urlencoded {
        s, err := strconv.ParseInt(r.Header.Get(headerLength), 10, 0)
        if err != nil { // headerLength在使用golang的http.post时会被去掉
            s, err = strconv.ParseInt(r.Header.Get(janbarLength), 10, 0)
            if err != nil {
                return err
            }
        }
        fr, size, path = r.Body, s, filepath.Join(basePath, r.URL.Path)
    } else {
        rf, rh, err := r.FormFile("file")
        if err != nil {
            return err
        }
        fr, size, path = rf, rh.Size, filepath.Join(basePath, r.URL.Path, rh.Filename)
    }
    defer fr.Close()

    fw, err := os.Create(path)
    if err != nil {
        return err
    }

    pw := handleWriteData(fw, nil, "POST>"+path, size)
    _, err = io.CopyBuffer(pw, fr, buf)
    fw.Close() // 趁早写入文件
    pw.Close()
    if err != nil {
        return err
    }
    w.Write(respOk)
    return nil
}

func handleGetFile(w http.ResponseWriter, r *http.Request, buf []byte) error {
    path := filepath.Join(basePath, r.URL.Path)
    fi, err := os.Stat(path)
    if err != nil {
        return err
    }

    if fi.IsDir() {
        if useEncrypt { // 加密方式不支持浏览目录,懒得写前端代码
            return errors.New("encrypt method not support list dir")
        }
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

        link := bytes.NewBuffer(buf[1024:])
        for i, v := range dir {
            w.Write(htmlTrTd)
            w.Write(strconv.AppendInt(buf[:0], int64(i+1), 10))
            w.Write(htmlTdTd)

            link.Reset()
            link.WriteString(url.PathEscape(v.Name()))
            if v.IsDir() {
                w.Write(htmlDir)
                link.WriteByte('/')
            } else {
                w.Write(htmlFile)
            }

            w.Write(htmlTdTd)
            w.Write(convertByte(buf[:0], v.Size()))
            w.Write(htmlTdTd)
            w.Write(v.ModTime().AppendFormat(buf[:0], timeLayout))
            w.Write(htmlTdTdA)
            w.Write(link.Bytes())
            w.Write(htmlGt)
            w.Write([]byte(v.Name()))
            w.Write(htmlAtdTr)
        }
        w.Write(htmlSuffix)
    } else {
        pw := handleWriteData(w, nil, "GET >"+path, fi.Size())
        if useEncrypt {
        }
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
    htmlIndex   = [6]int{172, 252, 340, 420, 508, 588} // 插入checked位置
    htmlPrefix  = []byte(`<html lang="zh"><head><title>list dir</title></head><body><div style="position:fixed;bottom:20px;right:10px">
<p><label><input type="radio" name="sort" onclick="sortDir(0)">名称升序</label><label><input type="radio" name="sort" onclick="sortDir(1)">名称降序</label></p>
<p><label><input type="radio" name="sort" onclick="sortDir(2)">时间升序</label><label><input type="radio" name="sort" onclick="sortDir(3)">时间降序</label></p>
<p><label><input type="radio" name="sort" onclick="sortDir(4)">大小升序</label><label><input type="radio" name="sort" onclick="sortDir(5)">大小降序</label></p>
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
    fs := flag.NewFlagSet(os.Args[0]+" cli", flag.ExitOnError)
    httpUrl := fs.String("u", "", "http url")
    data := fs.String("d", "", "post data")
    output := fs.String("o", "", "output")
    encrypt := fs.Bool("e", false, "encrypt data")
    fs.Parse(os.Args[2:])

    if *httpUrl == "" {
        return errors.New("url is null")
    }

    buf := bytePool.Get().([]byte)
    defer bytePool.Put(buf)
    if *data != "" {
        return clientPost(*data, *httpUrl, *encrypt, buf)
    }
    return clientGet(*httpUrl, *output, *encrypt, buf)
}

func clientPost(data, url string, encrypt bool, buf []byte) error {
    var (
        size int64
        body io.Reader
    )
    if len(data) >= 1 && data[0] == '@' {
        fr, err := os.Open(data[1:])
        if err != nil {
            return err
        }
        defer fr.Close()

        fi, err := fr.Stat()
        if err != nil {
            return err
        }
        size, body = fi.Size(), fr
    } else {
        sr := strings.NewReader(data)
        size, body = sr.Size(), sr
    }

    req, err := http.NewRequest(http.MethodPost, url, body)
    if err != nil {
        return err
    }
    req.Header.Set(headerType, urlencoded)
    req.Header.Set(janbarLength, string(strconv.AppendInt(buf[:0], size, 10)))

    resp, err := http.DefaultClient.Do(req)
    if err != nil {
        return err
    }
    if resp.Body != nil {
        _, err = io.CopyBuffer(os.Stdout, resp.Body, buf)
        resp.Body.Close()
    }
    return err
}

// http get客户端,支持断点下载
func clientGet(url, output string, encrypt bool, buf []byte) error {
    req, err := http.NewRequest(http.MethodGet, url, nil)
    if err != nil {
        return err
    }
    if output == "" {
        output = filepath.Base(req.URL.Path)
    }

    var (
        size int64
        fw   io.WriteCloser
    )
    fi, err := os.Stat(output)
    if err == nil {
        if fi.IsDir() {
            return errors.New(output + "is dir")
        }
        size = fi.Size()
        fw, err = os.OpenFile(output, os.O_APPEND, 0666)
    } else {
        fw, err = os.Create(output)
    }
    if err != nil {
        return err
    }
    defer fw.Close()

    if size > 0 { // 本地文件已存在,上传范围,请求断点续传
        req.Header.Set("Range", "bytes="+string(strconv.AppendInt(buf[:0], size, 10))+"-")
    }

    resp, err := http.DefaultClient.Do(req)
    if err != nil {
        return err
    }
    if resp.Body == nil {
        return errors.New("body is null")
    }
    defer resp.Body.Close()

    switch resp.StatusCode {
    case http.StatusOK, http.StatusPartialContent: // 完整接收,断点续传
    case http.StatusRequestedRangeNotSatisfiable:
        size, _ = io.CopyBuffer(ioutil.Discard, resp.Body, buf)
        fmt.Printf("[%d bytes data]\n", size) // 已经下载完毕
        return nil
    default:
        return errors.New(strconv.Itoa(resp.StatusCode) + "not support")
    }

    size, err = strconv.ParseInt(resp.Header.Get(headerLength), 10, 0)
    if err != nil {
        return err
    }

    pw := handleWriteData(fw, nil, "GET >"+output, size)
    _, err = io.CopyBuffer(pw, resp.Body, buf)
    pw.Close()
    return err
}

/*--------------------------------下面是工具类---------------------------------*/
const (
    sortDirTypeByNameAsc sortDirType = iota
    sortDirTypeByNameDesc
    sortDirTypeByTimeAsc
    sortDirTypeByTimeDesc
    sortDirTypeBySizeAsc
    sortDirTypeBySizeDesc
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
    lx, ly := len(d.fi[x].Name()), len(d.fi[y].Name())
    if lx == ly {
        return d.fi[x].Name() < d.fi[y].Name()
    }
    return lx < ly
}
func (d *dirInfoSort) Less(x, y int) bool {
    if d.fi[x].IsDir() != d.fi[y].IsDir() {
        return d.fi[x].IsDir()
    }
    switch d.sortType {
    default:
        fallthrough
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

type progress struct {
    cnt    int64
    rate   chan int64
    header http.Header

    writeHeader func(int)
    write       func([]byte) (int, error)

    cipher cipher.Stream
}

func handleWriteData(w interface{}, c cipher.Stream, prefix string, size int64) *progress {
    p := &progress{rate: make(chan int64), cipher: c}
    switch pw := w.(type) {
    case http.ResponseWriter:
        p.header, p.writeHeader, p.write = pw.Header(), pw.WriteHeader, pw.Write
    case io.Writer:
        p.write = pw.Write
    default:
        return nil
    }

    cnt := 0
    for tmp := size; tmp > 0; tmp /= 10 {
        cnt++
    }
    go func(rate <-chan int64, format string, size int64) {
        for cur := range rate {
            fmt.Printf(format, cur)
        }
        fmt.Printf(format+"\r\n", size)
    }(p.rate, fmt.Sprintf("\r%s [%%%dd - %d]", prefix, cnt, size), size)
    return p
}
func (p *progress) Header() http.Header { return p.header }
func (p *progress) WriteHeader(code int) {
    if p.writeHeader != nil {
        p.writeHeader(code)
    }
}
func (p *progress) Write(b []byte) (n int, err error) {
    if p.cipher != nil {
        p.cipher.XORKeyStream(b, b)
    }
    n, err = p.write(b)
    p.cnt += int64(n)
    select {
    case p.rate <- p.cnt:
    default:
    }
    return
}
func (p *progress) Close() { close(p.rate) }

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

/*--------------------------------加密工具类---------------------------------*/
