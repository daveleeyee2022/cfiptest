package speed

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

func (st *CFSpeedTest) TestDelay(ips []IpPair, locationMap map[string]Location) chan Result {
	var wg sync.WaitGroup

	resultChan := make(chan Result, len(ips))

	thread := make(chan struct{}, st.MaxThread)

	count := atomic.Int64{}
	okCount := atomic.Int64{}
	total := len(ips)

	for _, ip := range ips {
		// 如果满足延迟测试条数，则跳过
		if st.MaxDelayCount > 0 && okCount.Load() >= int64(st.MaxDelayCount) {
			break
		}
		wg.Add(1)
		thread <- struct{}{}
		go func(ipPair IpPair) {
			defer func() {
				<-thread
				count.Add(1)
				percentage := float64(count.Load()) / float64(total) * 100

				fmt.Printf("已完成: %d/%d(%.2f%%)，有效个数：%d", count.Load(), total, percentage, okCount.Load())
				if count.Load() == int64(total) {
					fmt.Printf("\n")
				} else {
					fmt.Printf("\r")
				}
				wg.Done()
			}()

			dialer := &net.Dialer{
				Timeout:   timeout,
				KeepAlive: 0,
			}
			start := time.Now()
			conn, err := dialer.Dial("tcp", net.JoinHostPort(ipPair.ip, strconv.Itoa(ipPair.port)))
			if err != nil {
				if st.VerboseMode {
					fmt.Printf("connect failed, ip: %s err: %s\n", ipPair.String(), err)
				}
				return
			}
			defer conn.Close()

			tcpDuration := time.Since(start)
			start = time.Now()

			client := http.Client{
				Transport: &http.Transport{
					TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, // 跳过证书验证
					Dial: func(network, addr string) (net.Conn, error) {
						return conn, nil
					},
				},
				Timeout: timeout,
			}

			var protocol string
			if st.EnableTLS {
				protocol = "https://"
			} else {
				protocol = "http://"
			}
			requestURL := protocol + st.DelayTestURL

			req, _ := http.NewRequest("GET", requestURL, nil)

			// 添加用户代理
			req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/123.0.0.0 Safari/537.36")
			req.Close = true
			ctx, cancel := context.WithTimeout(context.Background(), maxDuration)
			defer cancel()
			resp, err := client.Do(req.WithContext(ctx))
			if err != nil {
				if st.VerboseMode {
					fmt.Printf("http request failed, ip: %s err: %s\n", ipPair.String(), err)
				}
				return
			}
			defer resp.Body.Close()
			duration := time.Since(start)
			if duration > maxDuration {
				return
			}
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				return
			}

			if strings.Contains(string(body), "uag=Mozilla/5.0") {
				if matches := regexp.MustCompile(`colo=([A-Z]+)`).FindStringSubmatch(string(body)); len(matches) > 1 {
					dataCenter := matches[1]
					loc, ok := locationMap[dataCenter]
					if ok {
						fmt.Printf("发现有效IP %s 位置信息 %s 延迟 %d 毫秒\n", ipPair.String(), loc.City, tcpDuration.Milliseconds())
						resultChan <- Result{ipPair.ip, ipPair.port, dataCenter, loc.Region, loc.City, fmt.Sprintf("%d", tcpDuration.Milliseconds()), tcpDuration}
					} else {
						fmt.Printf("发现有效IP %s 位置信息未知 延迟 %d 毫秒\n", ipPair.String(), tcpDuration.Milliseconds())
						resultChan <- Result{ipPair.ip, ipPair.port, dataCenter, "", "", fmt.Sprintf("%d", tcpDuration.Milliseconds()), tcpDuration}
					}
					okCount.Add(1)
				}
			}

		}(ip)
	}

	wg.Wait()
	close(resultChan)
	if st.MaxDelayCount > 0 && okCount.Load() >= int64(st.MaxDelayCount) {
		fmt.Printf("已满足最大延迟测试个数，跳过剩下延迟测试，符合个数：%d\n", okCount.Load())
	}
	return resultChan
}
