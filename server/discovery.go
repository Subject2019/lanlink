package main

import (
	"encoding/json"
	"log"
	"net"
)

// 局域网自动发现：客户端向 255.255.255.255:8788 广播一行 JSON，
// 服务端回一个 {"v":1,"port":8787}，客户端用收到的包源 IP 拼出访问地址。
// 只有几十字节，开销可以忽略。
const discoveryMagic = "lanlink.discover"

type discoveryReply struct {
	V    int    `json:"v"`
	Port int    `json:"port"`
	Name string `json:"name"`
}

func startDiscovery(port int, httpPort int) (*net.UDPConn, error) {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: port})
	if err != nil {
		return nil, err
	}
	go func() {
		buf := make([]byte, 512)
		reply, _ := json.Marshal(discoveryReply{V: 1, Port: httpPort, Name: "LanLink"})
		for {
			n, addr, err := conn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			if string(buf[:n]) == discoveryMagic {
				if _, err := conn.WriteToUDP(reply, addr); err != nil {
					log.Printf("发现应答失败: %v", err)
				}
			}
		}
	}()
	return conn, nil
}
