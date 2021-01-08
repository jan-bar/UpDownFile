# UpDownFile
简易上传下载文件服务器，针对场景为临时需要上传或下载单个文件，完成后直接关闭服务器就完事了。

1. 可以使用url访问，会显示一个建议web页面，可以在这个web页面上传下载文件，以及进行文件的排序。  
2. 也可以使用wget或curl命令行工具上传下载文件，多种选择，总有一个是你想要的方式。  

执行：UpDownFile，会打印帮助文档，里面有使用curl和wget的上传下载文件命令。  
下载文件时会自动保存文件名为参数里面的basename。  
上传文件时会保存到url参数里面的文件路径。  
```bash
dir [C:\dir]

get file:
    wget --content-disposition "http://127.0.0.1:8080/dir/tmp.txt"
    curl -OJ "http://127.0.0.1:8080/dir/tmp.txt"
post file:
    wget -qO - --post-file=C:\tmp.txt "http://127.0.0.1:8080/dir/tmp.txt"
    curl --data-binary @C:\tmp.txt "http://127.0.0.1:8080/dir/tmp.txt"
    curl -F "file=@C:\tmp.txt" "http://127.0.0.1:8080/dir/"
```
