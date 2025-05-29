package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
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

type BatchIPInfo struct {
	Query   string `json:"query"`
	Country string `json:"country"`
}

var (
	totalChecked int
	totalFound   int
	mutex        sync.Mutex
	proxySet     = make(map[string]bool) // Track unique proxies
)

const (
	maxWorkers      = 10000           // Increased for faster processing
	timeoutDuration = 3 * time.Second // Reduced timeout
	testURL         = "http://example.com"
	batchSize       = 100           // ip-api.com allows 45 IPs per batch
)

func main() {
	proto := ask("Choose protocol (http, socks4, socks5, all): ")
	country := strings.ToUpper(ask("Choose country (e.g., VN, DE, EN, ALL): "))

	fmt.Println("Fetching proxies...")
	var allProxies []ProxyResult
	var wg sync.WaitGroup

	// Fetch proxy lists concurrently
	proxiesChan := make(chan []ProxyResult, len(httpSources)+len(socks4Sources)+len(socks5Sources))
	if proto == "http" || proto == "all" {
		for _, src := range httpSources {
			wg.Add(1)
			go func(src string) {
				defer wg.Done()
				proxiesChan <- fetchProxies(src, "http")
			}(src)
		}
	}
	if proto == "socks4" || proto == "all" {
		for _, src := range socks4Sources {
			wg.Add(1)
			go func(src string) {
				defer wg.Done()
				proxiesChan <- fetchProxies(src, "socks4")
			}(src)
		}
	}
	if proto == "socks5" || proto == "all" {
		for _, src := range socks5Sources {
			wg.Add(1)
			go func(src string) {
				defer wg.Done()
				proxiesChan <- fetchProxies(src, "socks5")
			}(src)
		}
	}

	// Collect fetched proxies
	go func() {
		wg.Wait()
		close(proxiesChan)
	}()
	for proxies := range proxiesChan {
		allProxies = append(allProxies, proxies...)
	}

	// Deduplicate proxies early
	uniqueProxies := deduplicateProxies(allProxies)
	fmt.Printf("Fetched %d unique proxies. Checking...\n", len(uniqueProxies))

	proxyChan := make(chan ProxyResult, maxWorkers)
	var checkWg sync.WaitGroup

	// Start workers
	for i := 0; i < maxWorkers; i++ {
		checkWg.Add(1)
		go worker(proxyChan, &checkWg, country)
	}

	// Send proxies to workers
	for _, p := range uniqueProxies {
		proxyChan <- p
	}
	close(proxyChan)
	checkWg.Wait()

	fmt.Println("\nScan complete.")
}

func worker(proxyChan <-chan ProxyResult, wg *sync.WaitGroup, country string) {
	defer wg.Done()
	var proxies []ProxyResult
	for p := range proxyChan {
		if checkProxy(p) {
			proxies = append(proxies, p)
		}
		mutex.Lock()
		totalChecked++
		if totalChecked%10 == 0 { // Update stats less frequently
			printStats(country, p.Proto)
		}
		mutex.Unlock()

		// Process in batches
		if len(proxies) >= batchSize {
			processBatch(proxies, country)
			proxies = proxies[:0]
		}
	}
	// Process remaining proxies
	if len(proxies) > 0 {
		processBatch(proxies, country)
	}
}

func processBatch(proxies []ProxyResult, country string) {
	if len(proxies) == 0 {
		return
	}

	// Batch IP info lookup
	ips := make([]string, 0, len(proxies))
	for _, p := range proxies {
		parts := strings.Split(p.Address, ":")
		if len(parts) > 0 {
			ips = append(ips, parts[0])
		}
	}
	infoMap := getBatchIPInfo(ips)

	mutex.Lock()
	defer mutex.Unlock()
	for _, p := range proxies {
		parts := strings.Split(p.Address, ":")
		if len(parts) < 1 {
			continue
		}
		info, exists := infoMap[parts[0]]
		if exists && info.Country != "" && (strings.ToUpper(info.Country) == country || country == "ALL") {
			if !proxySet[p.Address] {
				proxySet[p.Address] = true
				saveProxy(p.Proto, strings.ToUpper(info.Country), p.Address)
				totalFound++
			}
		}
	}
}

func ask(prompt string) string {
	fmt.Print(prompt)
	reader := bufio.NewReader(os.Stdin)
	text, _ := reader.ReadString('\n')
	return strings.TrimSpace(text)
}

func fetchProxies(src, proto string) []ProxyResult {
	var results []ProxyResult
	client := &http.Client{Timeout: timeoutDuration}
	resp, err := client.Get(src)
	if err != nil {
		fmt.Printf("Failed to fetch %s: %v\n", src, err)
		return results
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	lines := strings.Split(string(body), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		line = strings.TrimPrefix(line, proto+"://")
		if _, err := net.ResolveTCPAddr("tcp", line); err == nil {
			results = append(results, ProxyResult{Address: line, Proto: proto})
		}
	}
	return results
}

func deduplicateProxies(proxies []ProxyResult) []ProxyResult {
	seen := make(map[string]ProxyResult)
	for _, p := range proxies {
		if _, exists := seen[p.Address]; !exists {
			seen[p.Address] = p
		}
	}
	var unique []ProxyResult
	for _, p := range seen {
		unique = append(unique, p)
	}
	return unique
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

	req, err := http.NewRequest("HEAD", testURL, nil)
	if err != nil {
		return false
	}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == 200
}

func getBatchIPInfo(ips []string) map[string]BatchIPInfo {
	infoMap := make(map[string]BatchIPInfo)
	if len(ips) == 0 {
		return infoMap
	}

	// Prepare batch request
	data := make(map[string][]string)
	data["queries"] = ips
	body, _ := json.Marshal(data)

	client := &http.Client{Timeout: timeoutDuration}
	resp, err := client.Post("http://ip-api.com/batch", "application/json", bytes.NewReader(body))
	if err != nil || resp.StatusCode != 200 {
		return infoMap
	}
	defer resp.Body.Close()

	var infos []BatchIPInfo
	err = json.NewDecoder(resp.Body).Decode(&infos)
	if err != nil {
		return infoMap
	}

	for _, info := range infos {
		if info.Country != "" {
			infoMap[info.Query] = info
		}
	}
	return infoMap
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
