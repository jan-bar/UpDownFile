
set CGO_ENABLED=0
set GOARCH=amd64
set GOOS=linux
go build -ldflags "-s -w" -trimpath -o upDownFile.linux
set GOOS=windows
go build -ldflags "-s -w" -trimpath -o upDownFile.exe

where /q upx
if %errorlevel% equ 0 (
  upx -9 upDownFile.linux upDownFile.exe
)
