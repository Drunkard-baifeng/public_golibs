package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/Drunkard-baifeng/public_golibs/pop3client"
)

func Pop3ClientCall() {
	emailUsername := "leiyoutang1@163.com"
	emailPassword := "FAAxBaWDsNpkfhGZ"

	// proxyHost := "106.227.76.232"
	// proxyPort := 10687
	// proxyUsername := "faw5658"
	// proxyPassword := "faw5658"
	proxyHost := "112.28.227.16"
	proxyPort := 26403
	proxyUsername := ""
	proxyPassword := ""

	client, err := pop3client.NewEmailClientWithConfig(pop3client.EmailConfig{
		Username:      emailUsername,
		Password:      emailPassword,
		ProxyType:     pop3client.ProxyHTTP,
		ProxyHost:     proxyHost,
		ProxyPort:     proxyPort,
		ProxyUsername: proxyUsername,
		ProxyPassword: proxyPassword,
		Timeout:       30 * time.Second,
	})
	if err != nil {
		fmt.Println("NewEmailClientWithConfig failed:", err)
		return
	}
	defer client.Close()

	if err = client.Connect(); err != nil {
		fmt.Println("Connect failed:", err)
		return
	}
	fmt.Println("POP3 connected")

	count, size, err := client.GetEmailStats()
	if err != nil {
		fmt.Println("GetEmailStats failed:", err)
		return
	}
	fmt.Printf("mail stats: count=%d size=%d bytes\n", count, size)

	if count <= 0 {
		fmt.Println("no email")
		return
	}

	lastEmail, err := client.GetEmailContent(count)
	if err != nil {
		fmt.Println("GetEmailContent failed:", err)
		return
	}

	fmt.Println("last email from:", lastEmail.From)
	fmt.Println("last email subject:", lastEmail.Subject)
	fmt.Println("last email date:", lastEmail.Date)

	body := strings.TrimSpace(lastEmail.Body)
	if body == "" {
		body = strings.TrimSpace(lastEmail.HtmlBody)
	}
	// if len(body) > 300 {
	// 	body = body[:300] + "..."
	// }
	fmt.Println("last email preview:", body)
}
