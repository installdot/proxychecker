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
)

const maxWorkers = 10000

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
			if info != nil && (strings.ToUpper(info.Country) == country || country == "ALL") {
				saveProxy(p.Proto, info.Country, p.Address)
				mutex.Lock()
				totalFound++
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
		resp, err := http.Get(src)
		if err != nil {
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
			results = append(results, ProxyResult{Address: line, Proto: proto})
		}
	}
	return results
}

func checkProxy(p ProxyResult) bool {
	timeout := 4 * time.Second
	switch p.Proto {
	case "http":
		conn, err := net.DialTimeout("tcp", p.Address, timeout)
		if err == nil {
			conn.Close()
			return true
		}
	case "socks4", "socks5":
		dialer, err := proxy.SOCKS5("tcp", p.Address, nil, &net.Dialer{Timeout: timeout})
		if err != nil {
			return false
		}
		conn, err := dialer.Dial("tcp", "example.com:80")
		if err == nil {
			conn.Close()
			return true
		}
	}
	return false
}

func getIPInfo(ip string) *IPInfo {
	parts := strings.Split(ip, ":")
	if len(parts) < 1 {
		return nil
	}
	resp, err := http.Get("http://ip-api.com/json/" + parts[0])
	if err != nil {
		return nil
	}
	defer resp.Body.Close()

	var info IPInfo
	err = json.NewDecoder(resp.Body).Decode(&info)
	if err != nil {
		return nil
	}
	return &info
}

func saveProxy(proto, country, address string) {
	os.MkdirAll("proxies", 0755)
	fileName := fmt.Sprintf("proxies/%s_%s_proxy.txt", strings.ToUpper(country), proto)
	f, _ := os.OpenFile(fileName, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
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
