# UpDownFile
简易上传下载文件服务器，针对场景为临时需要上传或下载单个文件，完成后就不需要服务器功能。

1. 模拟一个http服务器，通过curl和wget命令作为客户端实现文件的上传下载功能。  
2. 只是实现一个小工具，所以没必要使用http库了，我也试过用http库来完成相同的功能，发现很多东西都用不上。  
3. 上传和下载文件加入了进度显示，方便知道上传和下载进度。本来想实现断点续传功能，但比较懒，不想弄，原理很简单。  

执行：UpDownFile，会打印帮助文档，里面有使用curl和wget的上传下载文件命令。  
下载文件时会自动保存文件名为参数里面的basename。  
上传文件时会保存到url参数里面的文件路径。  
```bash
usage: UpDownFile ip:port

get file:
  wget --content-disposition "http://ip:port?/root/tmp.txt"
  curl -OJ "http://ip:port?/root/tmp.txt"
post file:
  wget -q -O - --post-file=d:\tmp.txt "http://ip:port?/root/tmp.txt"
  curl "http://ip:port?/root/tmp.txt" --data-binary @d:\tmp.txt
```
