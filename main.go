package main

import (
	_ "embed"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
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

const (
	fileMode = 0o666
	flagW    = os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	flagA    = os.O_CREATE | os.O_WRONLY | os.O_APPEND
)

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

func InternalIp() (ips []net.IP) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return
	}

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
				ips = append(ips, ipNet.IP)
			}
		}
	}
	return
}

type poolByte struct {
	buf []byte
}

var bytePool = sync.Pool{New: func() any {
	return &poolByte{buf: make([]byte, 32<<10)}
}}

var (
	defaultByteUnit = []struct {
		unit string
		byte float64
	}{
		{byte: 1}, // 大于TB可能超过float64限制
		{byte: 1 << 10, unit: "B"},
		{byte: 1 << 20, unit: "KB"},
		{byte: 1 << 30, unit: "MB"},
		{byte: 1 << 40, unit: "GB"},
		{byte: 1 << 50, unit: "TB"},
	}
	byteUnitFormat = []string{"%.2f%s", "%.3f%s", "%7.2f%s", "%8.3f%s"}
)

func convertByte(size int64, fill bool) string {
	// fill=true时,需要保证返回字符串长度为9,主要是为了外部对齐

	b := float64(size)
	if b <= 0 {
		b = 0 // size=0 或 超过float64
		index := 1
		if fill {
			index |= 2
		}
		return fmt.Sprintf(byteUnitFormat[index], b, defaultByteUnit[1].unit)
	}

	for i := 1; i < len(defaultByteUnit); i++ {
		if b < defaultByteUnit[i].byte {
			var (
				index int
				unit  = defaultByteUnit[i].unit
			)
			if fill {
				index |= 2 // 左边填充空白
			}
			if unit == defaultByteUnit[1].unit {
				index |= 1 // 为了保持长度,小数取3位
			}
			return fmt.Sprintf(byteUnitFormat[index], b/defaultByteUnit[i-1].byte, unit)
		}
	}
	return "OverLimit"
}
