## UpDownFile
简易上传下载文件服务器，针对场景为临时需要上传或下载单个文件，完成后直接关闭服务器就完事了。

1. 可以使用url访问，会显示一个简易web页面，可以在这个web页面**上传下载**文件，以及进行文件的排序。  
2. 也可以使用wget或curl命令行工具上传下载文件，多种选择，总有一个是你想要的方式。  
3. 增加秘钥选项，使用后上传下载传输的数据全都加密，且秘钥每次都随机，有crc和时间戳校验，安全性极高。  
4. 使用加密选项时，无法通过web展示目录页面（前端加密代码不想写），可通过本程序命令行实现加密上传下载。  
5. 本工具作为客户端时可以实现断点上传或断点下载。提示里面有服务器和客户端命令行，可以参考。  
6. 成功执行后会显示帮助命令，可以复制改改就能用，非常方便。  
7. wget用`-c`,curl用`-C -`可以实现断点下载,可使用`curl -C - -T`实现断点上传。我的cli命令行使用`-c`可以支持断点**上传**和**下载**。  
8. 可以执行`.\UpDownFile -reg -s 127.0.0.1:8080`在同级目录下产生`addRightClickRegistry.reg`的注册表文件,双击reg文件添加右键菜单。  

![生成右键菜单](RightClick.png)

9. web页面展示如下图所示,支持断点下载,支持web直接上传：

![展示Web页面](ShowWeb.png)

10. 确保文件正确性,增加计算md5值功能,服务器端会自动计算md5值,使用cli工具时也会计算md5值,包括加密传输时也会正确计算md5值。
```bash
GET >e:\1.txt 100% 9d684b1f28fbde1b730681673d83530e
GET >e:\2.jpg 100% f7d3bb804a1fbb12b8eff77785a1bc4c
POST>e:\dos2unix 100% 3a7237e306544a12b5d0438fadc55f03
```
11. 加密的秘钥包含时间戳,只允许10秒内的有效秘钥(秘钥随机生成),安全性高。  

执行：`UpDownFile -h`，可查看服务端帮助信息：
```shell
Usage of UpDownFile:
  -e string
        password
  -p string
        path (default ".")
  -reg
        add right click registry
  -s string
        ip:port
  -t duration
        server timeout (default 30s)
```

执行：`UpDownFile cli -h`，可查看客户端帮助信息：
```shell
Usage of UpDownFile cli:
  -c    Resumed transfer offset
  -d string
        <raw string> or @tmp.txt
  -e string
        password
  -o string
        output
  -t duration
        client timeout (default 1m0s)
```

执行：`UpDownFile`会打印辅助信息，里面有使用curl和wget的上传下载文件命令。  
```bash
UpDownFile -s 127.0.0.1:8080 -p C:\dir -e password

url: http://127.0.0.1:8080

server:
    UpDownFile -s 127.0.0.1:8080 -p C:\dir -t 30s -e password
registry:
    UpDownFile -s 127.0.0.1:8080 -reg
cli get:
    UpDownFile cli -c -e password http://127.0.0.1:8080/tmp.txt
cli post:
    UpDownFile cli -c -e password -d @C:\tmp.txt http://127.0.0.1:8080/tmp.txt

Get File:
    wget -c --content-disposition http://127.0.0.1:8080/tmp.txt
    curl -C - -OJ http://127.0.0.1:8080/tmp.txt

Post File:
    wget -qO - --post-file=C:\tmp.txt http://127.0.0.1:8080/tmp.txt
    curl --data-binary @C:\tmp.txt http://127.0.0.1:8080/tmp.txt
    curl -F "file=@C:\tmp.txt" http://127.0.0.1:8080/

Get Offset:
    curl -H "Content-Type:application/janbar" http://127.0.0.1:8080/tmp.txt
    wget -qO - --header "Content-Type:application/janbar" http://127.0.0.1:8080/tmp.txt

Put File:
    curl -C - -T C:\tmp.txt http://127.0.0.1:8080/tmp.txt
```

## 使用详解
正常的上传下载没啥可说的,可以通过web页面也可以通过curl或wget命令。  
但是断点上传的功能我只找到了curl命令支持,不过需要指定上传断点位置。  
因此可以使用如下命令得到curl断点上传命令,直接执行返回的命令就可以断点上传：  
```shell
curl -H "Content-Type:application/janbar" http://127.0.0.1:8080/tmp.txt
curl -C 9662 -T file http://127.0.0.1:8080/tmp.txt

wget -qO - --header "Content-Type:application/janbar" http://127.0.0.1:8080/tmp.txt
curl -C 9662 -T file http://127.0.0.1:8080/tmp.txt

curl -C - -T tmp.txt  http://127.0.0.1:8080/tmp.txt
curl -C 9662 -T file http://127.0.0.1:8080/tmp.txt
```
cli支持断点上传,只需带上`-c`参数即可,内部实现自动获取断点位置,一步到位：
```shell
UpDownFile cli -c -d @tmp.txt http://127.0.0.1:8080/tmp.txt
```
断点下载`curl`,`wget`,以及我的cli工具均支持,没啥好说的,都在示例里面。

## 总结
该工具主要方便我传输文件使用,例如客户端是过了好几个跳板机的设备,那么我只需要在服务器上  
运行该工具,就可以在客户端上方便的传输文件。并且支持加密传输，也不怕被人拦截，  
秘钥只有10秒有效，可以防止中间人模拟请求搞攻击。
