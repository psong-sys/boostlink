// Command gen regenerates pb/exvs and pb/mms from the protocol description a game
// build carries inside vsac29_Release.exe, so exvs-online always matches the
// newest build. Needs protoc-gen-go v1.36.6 on PATH:
//
//	go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.6
//	go run ./pb/gen <game folder>\vsac29_Release.exe
//
// Run it from the exvs-online folder.
package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/pluginpb"
)

var targets = map[string]string{
	"game_server_nue.proto":        "exvs",
	"matchmaking_server_nue.proto": "mms",
}

func embedded(exe []byte, name string) (*descriptorpb.FileDescriptorProto, error) {
	key := append([]byte{0x0a, byte(len(name))}, name...)
	i := bytes.Index(exe, key)
	if i < 0 {
		return nil, fmt.Errorf("%s not found in the exe", name)
	}
	b := exe[i:]
	n := 0
	for n < len(b) {
		num, typ, l := protowire.ConsumeTag(b[n:])
		if l < 0 || num < 1 || num > 20 {
			break
		}
		vl := protowire.ConsumeFieldValue(num, typ, b[n+l:])
		if vl < 0 {
			break
		}
		n += l + vl
	}
	var fd descriptorpb.FileDescriptorProto
	return &fd, proto.Unmarshal(b[:n], &fd)
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: go run ./pb/gen <path to vsac29_Release.exe>")
		os.Exit(2)
	}
	exe, err := os.ReadFile(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	for name, pkg := range targets {
		fd, err := embedded(exe, name)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		req, _ := proto.Marshal(&pluginpb.CodeGeneratorRequest{
			FileToGenerate: []string{name},
			Parameter:      proto.String("M" + name + "=exvsonline/pb/" + pkg + ",paths=source_relative"),
			ProtoFile:      []*descriptorpb.FileDescriptorProto{fd},
		})
		cmd := exec.Command("protoc-gen-go")
		cmd.Stdin, cmd.Stderr = bytes.NewReader(req), os.Stderr
		out, err := cmd.Output()
		if err != nil {
			fmt.Fprintln(os.Stderr, "protoc-gen-go:", err)
			os.Exit(1)
		}
		var resp pluginpb.CodeGeneratorResponse
		if err := proto.Unmarshal(out, &resp); err != nil || resp.Error != nil {
			fmt.Fprintln(os.Stderr, "protoc-gen-go failed:", err, resp.GetError())
			os.Exit(1)
		}
		for _, f := range resp.File {
			dst := filepath.Join("pb", pkg, f.GetName())
			if err := os.WriteFile(dst, []byte(f.GetContent()), 0644); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			fmt.Println("wrote", dst)
		}
	}
}
