
set CGO_ENABLED=0
set ldflags="-buildid=janbar -s -w"

set GOOS=linux
set GOARCH=arm64
go build -ldflags %ldflags% -trimpath -o upDownFile.arm64
set GOARCH=amd64
go build -ldflags %ldflags% -trimpath -o upDownFile.amd64
set GOOS=darwin
go build -ldflags %ldflags% -trimpath -o upDownFile.mac
set GOOS=windows
go build -ldflags %ldflags% -trimpath -o upDownFile.exe
