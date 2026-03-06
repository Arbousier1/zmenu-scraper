package main

import (
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/gocolly/colly"
)

func main() {
	// 1. 初始化 Colly 收集器
	c := colly.NewCollector(
		colly.AllowedDomains("docs.groupez.dev"),
	)

	var docContent []string

	// 2. 提取网页标题
	c.OnHTML("title", func(e *colly.HTMLElement) {
		docContent = append(docContent, fmt.Sprintf("标题: %s\n", strings.TrimSpace(e.Text)))
	})

	// 3. 提取文档正文
	// 注意: 不同的静态网站生成器标签不同。通常文档在 <main> 或 <article> 中。
	// 这里我们抓取 <main> 标签内的所有文本。
	c.OnHTML("main", func(e *colly.HTMLElement) {
		text := strings.TrimSpace(e.Text)
		// 简单清理一下多余的空行
		docContent = append(docContent, text)
	})

	// 打印请求日志，方便在 GitHub Actions 的控制台中查看进度
	c.OnRequest(func(r *colly.Request) {
		fmt.Println("正在访问:", r.URL.String())
	})

	c.OnError(func(r *colly.Response, err error) {
		log.Println("请求失败:", r.Request.URL, "失败原因:", err)
	})

	// 4. 开始访问目标 URL
	targetURL := "https://docs.groupez.dev/zmenu/getting-started/"
	err := c.Visit(targetURL)
	if err != nil {
		log.Fatal(err)
	}

	// 5. 将结果写入本地文件
	finalText := strings.Join(docContent, "\n")
	err = os.WriteFile("output.txt", []byte(finalText), 0644)
	if err != nil {
		log.Fatal("写入文件失败:", err)
	}

	fmt.Println("爬取完成！已保存至 output.txt")
}