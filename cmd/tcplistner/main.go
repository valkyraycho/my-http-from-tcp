package main

import (
	"fmt"
	"log"
	"net"

	"github.com/valkyraycho/my-http-from-tcp/internal/request"
)

func main() {
	listener, err := net.Listen("tcp", ":42069")
	if err != nil {
		log.Fatal("error", err)
	}

	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Fatal("error", err)
		}

		r, err := request.RequestFromReader(conn)
		if err != nil {
			log.Fatal("error", err)
		}

		fmt.Println("Request Line:")
		fmt.Printf("- Method: %s\n", r.RequestLine.Method)
		fmt.Printf("- Request Target: %s\n", r.RequestLine.RequestTarget)
		fmt.Printf("- HTTP Version: %s\n", r.RequestLine.HttpVersion)
	}

}
