
set CGO_ENABLED=0
set GOARCH=amd64
set GOOS=linux
set ldflags="-buildid=janbar -s -w"

go build -ldflags %ldflags% -trimpath -o upDownFile.linux
set GOOS=windows
go build -ldflags %ldflags% -trimpath -o upDownFile.exe

where /q upx
if %errorlevel% equ 0 (
  upx -9 upDownFile.linux upDownFile.exe
)
