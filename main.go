package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
	"os/exec"
	"runtime"

	"golang.org/x/net/proxy"
)

var (
	httpSources = []string{
		"https://api.proxyscrape.com/v4/free-proxy-list/get?request=display_proxies&protocol=http&proxy_format=ipport&format=text&timeout=20000",
		"https://raw.githubusercontent.com/TheSpeedX/SOCKS-List/master/http.txt",
		"https://raw.githubusercontent.com/proxifly/free-proxy-list/refs/heads/main/proxies/protocols/http/data.txt",
		"https://raw.githubusercontent.com/MuRongPIG/Proxy-Master/refs/heads/main/http.txt",
		"https://raw.githubusercontent.com/vakhov/fresh-proxy-list/refs/heads/master/http.txt",
		"https://raw.githubusercontent.com/officialputuid/KangProxy/refs/heads/KangProxy/http/http.txt",
		"https://raw.githubusercontent.com/gfpcom/free-proxy-list/refs/heads/main/list/http.txt",
		"https://raw.githubusercontent.com/zebbern/Proxy-Scraper/refs/heads/main/http.txt",
	}
	socks4Sources = []string{
		"https://raw.githubusercontent.com/proxifly/free-proxy-list/refs/heads/main/proxies/protocols/socks4/data.txt",
		"https://raw.githubusercontent.com/MuRongPIG/Proxy-Master/refs/heads/main/socks4.txt",
		"https://raw.githubusercontent.com/dpangestuw/Free-Proxy/refs/heads/main/socks4_proxies.txt",
		"https://raw.githubusercontent.com/gfpcom/free-proxy-list/refs/heads/main/list/socks4.txt",
		"https://raw.githubusercontent.com/zebbern/Proxy-Scraper/refs/heads/main/socks4.txt",
	}
	socks5Sources = []string{
		"https://raw.githubusercontent.com/proxifly/free-proxy-list/refs/heads/main/proxies/protocols/socks5/data.txt",
		"https://raw.githubusercontent.com/MuRongPIG/Proxy-Master/refs/heads/main/socks5.txt",
		"https://raw.githubusercontent.com/dpangestuw/Free-Proxy/refs/heads/main/socks5_proxies.txt",
		"https://raw.githubusercontent.com/gfpcom/free-proxy-list/refs/heads/main/list/socks5.txt",
		"https://raw.githubusercontent.com/zebbern/Proxy-Scraper/refs/heads/main/socks5.txt",
	}
)

type IPInfo struct {
	Country string `json:"country"`
}

type ProxyResult struct {
	Address string
	Proto   string
}

var (
	totalChecked int
	totalFound   int
	mutex        sync.Mutex
	proxySet     = make(map[string]bool) // Track unique proxies
)

const (
	maxWorkers      = 100
	timeoutDuration = 5 * time.Second
	testURL         = "http://example.com"
)

func main() {
	proto := ask("Choose protocol (http, socks4, socks5, all): ")
	country := strings.ToUpper(ask("Choose country (e.g., VN, DE, EN, ALL): "))

	var allProxies []ProxyResult
	fmt.Println("Fetching proxies...")

	if proto == "http" || proto == "all" {
		allProxies = append(allProxies, fetchProxies(httpSources, "http")...)
	}
	if proto == "socks4" || proto == "all" {
		allProxies = append(allProxies, fetchProxies(socks4Sources, "socks4")...)
	}
	if proto == "socks5" || proto == "all" {
		allProxies = append(allProxies, fetchProxies(socks5Sources, "socks5")...)
	}

	fmt.Printf("Fetched %d proxies. Checking...\n", len(allProxies))

	proxyChan := make(chan ProxyResult, maxWorkers)
	var wg sync.WaitGroup

	for i := 0; i < maxWorkers; i++ {
		wg.Add(1)
		go worker(proxyChan, &wg, country)
	}

	for _, p := range allProxies {
		proxyChan <- p
	}
	close(proxyChan)
	wg.Wait()

	fmt.Println("\nScan complete.")
}

func worker(proxyChan <-chan ProxyResult, wg *sync.WaitGroup, country string) {
	defer wg.Done()
	for p := range proxyChan {
		if checkProxy(p) {
			info := getIPInfo(p.Address)
			if info != nil && info.Country != "" && (strings.ToUpper(info.Country) == country || country == "ALL") {
				mutex.Lock()
				if !proxySet[p.Address] { // Check for duplicates
					proxySet[p.Address] = true
					saveProxy(p.Proto, strings.ToUpper(info.Country), p.Address)
					totalFound++
				}
				mutex.Unlock()
			}
		}
		mutex.Lock()
		totalChecked++
		printStats(country, p.Proto)
		mutex.Unlock()
	}
}

func ask(prompt string) string {
	fmt.Print(prompt)
	reader := bufio.NewReader(os.Stdin)
	text, _ := reader.ReadString('\n')
	return strings.TrimSpace(text)
}

func fetchProxies(sources []string, proto string) []ProxyResult {
	var results []ProxyResult
	for _, src := range sources {
		client := &http.Client{Timeout: timeoutDuration}
		resp, err := client.Get(src)
		if err != nil {
			fmt.Printf("Failed to fetch %s: %v\n", src, err)
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		lines := strings.Split(string(body), "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			line = strings.TrimPrefix(line, proto+"://")
			if _, err := net.ResolveTCPAddr("tcp", line); err == nil { // Basic validation
				results = append(results, ProxyResult{Address: line, Proto: proto})
			}
		}
	}
	return results
}

func checkProxy(p ProxyResult) bool {
	client := &http.Client{
		Timeout: timeoutDuration,
	}

	switch p.Proto {
	case "http":
		client.Transport = &http.Transport{
			Proxy: http.ProxyURL(&url.URL{
				Scheme: "http",
				Host:   p.Address,
			}),
		}
	case "socks4", "socks5":
		dialer, err := proxy.SOCKS5("tcp", p.Address, nil, &net.Dialer{Timeout: timeoutDuration})
		if err != nil {
			return false
		}
		client.Transport = &http.Transport{
			Dial: dialer.Dial,
		}
	default:
		return false
	}

	resp, err := client.Get(testURL)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == 200
}

func getIPInfo(ip string) *IPInfo {
	parts := strings.Split(ip, ":")
	if len(parts) < 1 {
		return nil
	}

	client := &http.Client{Timeout: timeoutDuration}
	resp, err := client.Get("http://ip-api.com/json/" + parts[0])
	if err != nil || resp.StatusCode != 200 {
		return nil
	}
	defer resp.Body.Close()

	var info IPInfo
	err = json.NewDecoder(resp.Body).Decode(&info)
	if err != nil || info.Country == "" {
		return nil
	}
	return &info
}

func saveProxy(proto, country, address string) {
	os.MkdirAll("proxies", 0755)
	fileName := fmt.Sprintf("proxies/%s_%s_proxy.txt", country, proto)
	f, err := os.OpenFile(fileName, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		fmt.Printf("Failed to open file %s: %v\n", fileName, err)
		return
	}
	defer f.Close()
	f.WriteString(address + "\n")
}

func printStats(country, proto string) {
	clearConsole()
	line := fmt.Sprintf("Total Checked: %d | Country: %s | Type: %s | Live Found: %d",
		totalChecked, country, proto, totalFound)
	fmt.Println(line)
}

func clearConsole() {
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("cmd", "/c", "cls")
	} else {
		cmd = exec.Command("clear")
	}
	cmd.Stdout = os.Stdout
	cmd.Run()
}
