## UpDownFile
简易上传下载文件服务器，针对场景为临时需要上传或下载单个文件，完成后直接关闭服务器就完事了。

1. 可以使用url访问，会显示一个简易web页面，可以在这个web页面**上传下载**文件，以及进行文件的排序。  
2. 也可以使用wget或curl命令行工具上传下载文件，多种选择，总有一个是你想要的方式。  
3. 支持https方式启动服务器和客户端，传输内容加密，数据更安全  
4. 本工具作为客户端时可以实现断点上传或断点下载。提示里面有服务器和客户端命令行，可以参考。  
5. wget用`-c`,curl用`-C -`可以实现断点下载,可使用`curl -C - -T`实现断点上传。我的cli命令行使用`-c`可以支持断点**上传**和**下载**。  
6. 可以执行`.\UpDownFile -reg -s 127.0.0.1:8080`在同级目录下产生`addRightClickRegistry.reg`的注册表文件,双击reg文件添加右键菜单。  

![生成右键菜单](RightClick.png)

7. web页面展示如下图所示,支持断点下载,支持web直接上传：

![展示Web页面](ShowWeb.png)

8. 确保文件正确性,增加计算md5值功能,服务器端会自动计算md5值,使用cli工具时也会计算md5值,包括加密传输时也会正确计算md5值。
```bash
GET >e:\1.txt 100% 9d684b1f28fbde1b730681673d83530e
GET >e:\2.jpg 100% f7d3bb804a1fbb12b8eff77785a1bc4c
POST>e:\dos2unix 100% 3a7237e306544a12b5d0438fadc55f03
```

执行：`upDownFile -h`，可查看服务端帮助信息：
```shell
Usage of UpDownFile:
  -ca string
        ca file (default "ca.crt")
  -cert string
        cert file
  -key string
        key file
  -p string
        path (default ".")
  -reg
        add right click registry
  -s string
        ip:port
  -t duration
        read header timeout (default 1m0s)
```

执行：`upDownFile cli -h`，可查看客户端帮助信息：
```shell
Usage of UpDownFile cli:
  -c    resumed transfer offset
  -ca string
        ca.crt to verify peer against
  -d string
        <raw string> or @tmp.txt
  -g    gzip file to send
  -k    allow insecure server connections
  -o string
        output
  -t duration
        client timeout (default 1m0s)
```

执行：`UpDownFile`会打印辅助信息，里面有使用curl和wget的上传下载文件命令。  
```bash
upDownFile -s 127.0.0.1:8080 -p C:\dir -cert janbar.cert -key janbar.key

url: http://127.0.0.1:8080

server:
    upDownFile.exe -s 127.0.0.1:8080 -p C:\dir -t 1m0s -ca ca.crt -cert janbar.cert -key janbar.key
registry:
    upDownFile.exe -s 127.0.0.1:8080 -reg
cli get:
    upDownFile.exe cli -c -ca ca.crt "http://127.0.0.1:8080/tmp.txt"
cli post:
    upDownFile.exe cli -c -ca ca.crt -d @C:\tmp.txt "http://127.0.0.1:8080/tmp.txt"

Get File:
    wget --ca-certificate ca.crt -c --content-disposition "http://127.0.0.1:8080/tmp.txt"
    curl --cacert ca.crt -C - -OJ "http://127.0.0.1:8080/tmp.txt"

Post File:
    wget --ca-certificate ca.crt -qO - --post-file=C:\tmp.txt "http://127.0.0.1:8080/tmp.txt"
    wget --ca-certificate ca.crt -qO - --header="Content-Type: application/x-gzip" --post-file=C:\tmp.txt "http://127.0.0.1:8080/tmp.txt"
    curl --cacert ca.crt --data-binary @C:\tmp.txt "http://127.0.0.1:8080/tmp.txt"
    curl --cacert ca.crt -H "Content-Type: application/x-gzip" --data-binary @C:\tmp.txt "http://127.0.0.1:8080/tmp.txt"
    curl --cacert ca.crt -F "file=@C:\tmp.txt" "http://127.0.0.1:8080/"

Get Offset:
    curl --cacert ca.crt -H "Content-Type:application/offset" "http://127.0.0.1:8080/tmp.txt"
    wget --ca-certificate ca.crt -qO - --header "Content-Type:application/offset" "http://127.0.0.1:8080/tmp.txt"

Put File:
    curl --cacert ca.crt -C - -T C:\tmp.txt "http://127.0.0.1:8080/tmp.txt"
```

## 使用详解
正常的上传下载没啥可说的,可以通过web页面也可以通过curl或wget命令。  
但是断点上传的功能我只找到了curl命令支持,不过需要指定上传断点位置。  
因此可以使用如下命令得到curl断点上传命令,直接执行返回的命令就可以断点上传：  
```shell
# 获取服务器文件偏移
curl -H "Content-Type:application/offset" http://127.0.0.1:8080/tmp.txt
# 返回断点上传命令
curl -C 9662 -T file http://127.0.0.1:8080/tmp.txt

# 获取服务器文件偏移
wget -qO - --header "Content-Type:application/offset" http://127.0.0.1:8080/tmp.txt
# 返回断点上传命令
curl -C 9662 -T file http://127.0.0.1:8080/tmp.txt

# 全新上传文件
curl -C - -T tmp.txt http://127.0.0.1:8080/tmp.txt
# 从文件某个偏移上传到服务器
curl -C 9662 -T file http://127.0.0.1:8080/tmp.txt
```

cli支持断点上传,只需带上`-c`参数即可,内部实现自动获取断点位置,一步到位：
```shell
UpDownFile cli -c -d @tmp.txt http://127.0.0.1:8080/tmp.txt
```
断点下载`curl`,`wget`,以及我的cli工具均支持,没啥好说的,都在示例里面。

## 生成https证书
在Linux后台执行`./gen_cert.sh`,可以得到三个文件`ca.crt, janbar.key, janbar.cert`  
服务器需要两个文件启动服务: `janbar.key, janbar.cert`  
客户端需要`ca.crt`信任证书  

windows电脑按图所示添加ca根证书,之后浏览器访问`https://janbar.com`服务器就不会提示不安全  
![load_ca](load_ca.png)

## 使用gzip上传下载
使用`UpDownFile cli -g`启用gzip传输,减少传输时间,未知传输数据大小因此进度会不准  
上传时,客户端压缩gzip文件后上传压缩数据,服务器会自动解压gzip文件  
下载时,服务器压缩gzip文件后下载压缩数据,客户端会自动解压gzip文件  
注意: 使用gzip上传下载时,断点续传失效  

## 总结
该工具是我为了临时启用一个http服务器,客户端用`curl`,`wget`这类常见工具就能完成文件上传下载功能  
方便传输文件,客户端也可以是浏览器,满足各种使用场景
