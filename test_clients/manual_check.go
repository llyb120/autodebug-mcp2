package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os/exec"
)

func main() {
	cmd := exec.Command("../gomcp.exe")
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	cmd.Start()

	defer stdin.Close()
	defer stdout.Close()

	writer := stdin
	reader := bufio.NewReader(stdout)

	// 发送 initialize
	initReq := map[string]interface{}{
		"jsonrpc": "2.0",
		"id": 1,
		"method": "initialize",
		"params": map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"capabilities": map[string]interface{}{},
			"clientInfo": map[string]string{"name": "test", "version": "1.0"},
		},
	}
	reqData, _ := json.Marshal(initReq)
	writer.Write(reqData)
	writer.Write([]byte("\n"))

	// 读取响应
	line, _ := reader.ReadString('\n')
	fmt.Printf("Raw response: %s\n", line)

	var resp map[string]interface{}
	json.Unmarshal([]byte(line), &resp)
	fmt.Printf("Parsed response: %+v\n", resp)

	cmd.Process.Kill()
	cmd.Wait()
}
