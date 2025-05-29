package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// Configuration
const (
	BotToken       = "8034590926:AAHqqx76gz5tTT-P0TbnoBSX0swHS0C0Sfc"
	Channel        = "-1002520191258"
	Concurrency    = 100          // Number of concurrent proxy checks
	TimeoutSeconds = 2            // Timeout for proxy checks
)

var ProxyListURLs = []string{
	"https://raw.githubusercontent.com/TheSpeedX/SOCKS-List/master/http.txt",
	"https://raw.githubusercontent.com/proxifly/free-proxy-list/refs/heads/main/proxies/protocols/http/data.txt",
	"https://raw.githubusercontent.com/MuRongPIG/Proxy-Master/refs/heads/main/http.txt",
	"https://raw.githubusercontent.com/vakhov/fresh-proxy-list/refs/heads/master/http.txt",
	"https://raw.githubusercontent.com/officialputuid/KangProxy/refs/heads/KangProxy/http/http.txt",
	"https://raw.githubusercontent.com/gfpcom/free-proxy-list/refs/heads/main/list/http.txt",
	"https://raw.githubusercontent.com/zebbern/Proxy-Scraper/refs/heads/main/http.txt",
}

type ProxyResult struct {
	Proxy   string
	Country string
}

// FetchProxies downloads proxy lists and returns unique proxies
func FetchProxies() ([]string, error) {
	proxySet := make(map[string]struct{})
	var mu sync.Mutex

	var wg sync.WaitGroup
	semaphore := make(chan struct{}, Concurrency)

	for _, urlStr := range ProxyListURLs {
		wg.Add(1)
		go func(urlStr string) {
			defer wg.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			client := &http.Client{Timeout: time.Second * 5}
			resp, err := client.Get(urlStr)
			if err != nil {
				log.Printf("Error fetching %s: %v", urlStr, err)
				return
			}
			defer resp.Body.Close()

			scanner := bufio.NewScanner(resp.Body)
			count := 0
			for scanner.Scan() {
				line := strings.TrimSpace(scanner.Text())
				if line == "" || strings.HasPrefix(line, "#") {
					continue
				}
				// Remove http:// if present
				proxy := strings.TrimPrefix(line, "http://")
				mu.Lock()
				proxySet[proxy] = struct{}{}
				mu.Unlock()
				count++
			}
			log.Printf("Fetched %d proxies from %s", count, urlStr)
		}(urlStr)
	}

	wg.Wait()

	proxies := make([]string, 0, len(proxySet))
	for proxy := range proxySet {
		proxies = append(proxies, proxy)
	}
	return proxies, nil
}

// CheckProxy tests a single proxy and returns its country
func CheckProxy(proxy string) *ProxyResult {
	req, err := http.NewRequest("GET", "http://ipinfo.io/json", nil)
	if err != nil {
		log.Printf("Error creating request for %s: %v", proxy, err)
		return nil
	}

	proxyURL, err := url.Parse("http://" + proxy)
	if err != nil {
		log.Printf("Error parsing proxy URL %s: %v", proxy, err)
		return nil
	}

	transport := &http.Transport{
		Proxy: http.ProxyURL(proxyURL),
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   time.Second * TimeoutSeconds,
	}

	resp, err := client.Do(req)
	if err != nil {
		log.Printf("Proxy %s failed: %v", proxy, err)
		return nil
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("Error reading response for %s: %v", proxy, err)
		return nil
	}

	var data struct {
		Country string `json:"country"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		log.Printf("Error parsing JSON for %s: %v", proxy, err)
		return nil
	}

	return &ProxyResult{Proxy: proxy, Country: data.Country}
}

// SaveProxiesByCountry saves proxies to country-specific files
func SaveProxiesByCountry(proxyResults []ProxyResult) ([]string, error) {
	countryMap := make(map[string][]string)
	for _, result := range proxyResults {
		country := result.Country
		if country == "" {
			country = "Unknown"
		}
		countryMap[country] = append(countryMap[country], result.Proxy)
	}

	var savedFiles []string
	for country, proxies := range countryMap {
		fileName := fmt.Sprintf("%s_http_proxy.txt", country)
		data := strings.Join(proxies, "\n")
		if err := os.WriteFile(fileName, []byte(data), 0644); err != nil {
			log.Printf("Error saving %s: %v", fileName, err)
			continue
		}
		log.Printf("Saved %d proxies to %s", len(proxies), fileName)
		savedFiles = append(savedFiles, fileName)
	}
	return savedFiles, nil
}

// SendFilesToTelegram sends all proxy files to the Telegram channel
func SendFilesToTelegram(files []string) error {
	bot, err := tgbotapi.NewBotAPI(BotToken)
	if err != nil {
		return fmt.Errorf("failed to initialize Telegram bot: %v", err)
	}

	// Convert Channel string to int64
	chatID, err := strconv.ParseInt(Channel, 10, 64)
	if err != nil {
		return fmt.Errorf("failed to parse channel ID %s: %v", Channel, err)
	}

	for _, fileName := range files {
		file, err := os.Open(fileName)
		if err != nil {
			log.Printf("Error opening %s: %v", fileName, err)
			continue
		}
		defer file.Close()

		lines, err := countLines(fileName)
		if err != nil {
			log.Printf("Error counting lines in %s: %v", fileName, err)
			continue
		}

		country := strings.TrimSuffix(filepath.Base(fileName), "_http_proxy.txt")
		caption := fmt.Sprintf("HTTP Proxies for %s (%d proxies)", country, lines)

		doc := tgbotapi.NewDocument(chatID, tgbotapi.FilePath(fileName))
		doc.Caption = caption
		if _, err := bot.Send(doc); err != nil {
			log.Printf("Error sending %s to Telegram: %v", fileName, err)
			continue
		}
		log.Printf("Sent %s to Telegram channel", fileName)
	}
	return nil
}

// countLines counts the number of lines in a file
func countLines(filePath string) (int, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return 0, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	count := 0
	for scanner.Scan() {
		count++
	}
	return count, scanner.Err()
}

func main() {
	log.Println("Starting proxy checker...")
	proxies, err := FetchProxies()
	if err != nil {
		log.Fatalf("Failed to fetch proxies: %v", err)
	}
	log.Printf("Total unique proxies fetched: %d", len(proxies))

	// Check proxies concurrently
	semaphore := make(chan struct{}, Concurrency)
	results := make(chan ProxyResult, len(proxies))
	var wg sync.WaitGroup

	for _, proxy := range proxies {
		wg.Add(1)
		go func(p string) {
			defer wg.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			if result := CheckProxy(p); result != nil {
				results <- *result
			}
		}(proxy)
	}

	// Close results channel when all checks are done
	go func() {
		wg.Wait()
		close(results)
	}()

	// Collect results
	var validProxies []ProxyResult
	for result := range results {
		validProxies = append(validProxies, result)
	}

	log.Printf("Total valid proxies: %d", len(validProxies))
	savedFiles, err := SaveProxiesByCountry(validProxies)
	if err != nil {
		log.Printf("Error saving proxies: %v", err)
	}

	if err := SendFilesToTelegram(savedFiles); err != nil {
		log.Printf("Error sending files to Telegram: %v", err)
	}
	log.Println("Proxy checking, saving, and sending completed.")
}
