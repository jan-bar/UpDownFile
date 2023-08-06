package main

import (
	_ "embed"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
)

func main() {
	exe, err := os.Executable()
	if err != nil {
		panic(err)
	}

	if len(os.Args) >= 2 && os.Args[1] == "cli" {
		err = clientMain(exe, os.Args[2:])
	} else {
		err = serverMain(exe, os.Args[1:])
	}
	if err != nil {
		fmt.Printf("%+v\n", err)
	}
}

const fileMode = 0o666

//go:embed fileServer.ico
var icoData []byte // 嵌入图标文件

func createRegFile(exe, addr string) error {
	//goland:noinspection GoBoolExpressions
	if runtime.GOOS != "windows" {
		return nil // 仅window下才生成右键快捷键
	}

	fw, err := os.Create("addRightClickRegistry.reg")
	if err != nil {
		return err
	}
	//goland:noinspection GoUnhandledErrorResult
	defer fw.Close()

	icoFile := filepath.Join(filepath.Dir(exe), "fileServer.ico")
	err = os.WriteFile(icoFile, icoData, fileMode)
	if err != nil {
		return err
	}

	_, _ = fmt.Fprintf(fw, `Windows Registry Editor Version 5.00

[HKEY_CLASSES_ROOT\Directory\Background\shell\fileServer]
@="File Server Here"
"Icon"="%s"

[HKEY_CLASSES_ROOT\Directory\Background\shell\fileServer\command]
@="\"%s\" -s \"%s\" -p \"%%V\""
`, strings.ReplaceAll(icoFile, "\\", "\\\\"),
		strings.ReplaceAll(exe, "\\", "\\\\"), addr)
	return nil
}

func InternalIp() []string {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil
	}

	ips := make([]string, 0, len(interfaces))
	for _, inf := range interfaces {
		if inf.Flags&net.FlagUp != net.FlagUp ||
			inf.Flags&net.FlagLoopback == net.FlagLoopback {
			continue
		}

		addr, err := inf.Addrs()
		if err != nil {
			continue
		}

		for _, a := range addr {
			if ipNet, ok := a.(*net.IPNet); ok &&
				!ipNet.IP.IsLoopback() && ipNet.IP.To4() != nil {
				ips = append(ips, ipNet.IP.String())
			}
		}
	}
	return ips
}

type poolByte struct {
	buf []byte
}

var bytePool = sync.Pool{New: func() any {
	return &poolByte{buf: make([]byte, 32<<10)}
}}

var unitByte = []struct {
	byte float64
	unit string
}{
	{byte: 1},
	{byte: 1 << 10, unit: "B"},
	{byte: 1 << 20, unit: "KB"},
	{byte: 1 << 30, unit: "MB"},
	{byte: 1 << 40, unit: "GB"},
	{byte: 1 << 50, unit: "TB"},
}

func convertByte(buf []byte, b int64) string {
	tmp, unit := float64(b), "B"
	for i := 1; i < len(unitByte); i++ {
		if tmp < unitByte[i].byte {
			tmp /= unitByte[i-1].byte
			unit = unitByte[i].unit
			break
		}
	}
	return string(strconv.AppendFloat(buf, tmp, 'f', 2, 64)) + unit
}
