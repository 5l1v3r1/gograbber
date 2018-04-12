package lib

import (
	"bufio"
	"bytes"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"strings"
	"sync"
)

// func statusUpdater() {
// 	//update output every 3 seconds or so
// 	tick := time.Tick(time.Second * 3)
// }

func writerWorker(writeChan chan []byte, filename string) {
	file, err := os.OpenFile(filename, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if os.IsNotExist(err) {
		file, err = os.Create(filename)
	}
	if err != nil {
		panic(err)
	}
	writer := bufio.NewWriter(file)
	for {
		b := <-writeChan
		if len(b) > 0 {
			writer.Write(b)
			writer.Flush()
		}
	}
}

func headder(t string) (status int, length int64, err error) {
	req, err := http.NewRequest("HEAD", t+"/.git/index", nil)
	if err != nil {
		fmt.Println(strings.Repeat("~", 40))
		fmt.Println("Bad Req Construction")
		fmt.Println(err)
		fmt.Println(strings.Repeat("~", 40))
		return 0, 0, err
	}
	res, err := cl.Do(req)
	if res != nil && res.Body != nil {
		defer res.Body.Close()
	}
	if err != nil {
		return 0, 0, err
	}
	return res.StatusCode, res.ContentLength, nil
}

func getter(t string) (code int, body []byte, err error) {
	req, err := http.NewRequest("GET", t+"/.git/config", nil)
	if err != nil {
		fmt.Println(strings.Repeat("~", 40))
		fmt.Println("Bad Req Construction")
		fmt.Println(err)
		fmt.Println(strings.Repeat("~", 40))
	}

	//send off that request
	resp, err := cl.Do(req)
	if resp != nil {
		defer resp.Body.Close() //remember to close the body bro
	}
	if err != nil {
		return 404, nil, err
	}
	buf := &bytes.Buffer{}
	buf.ReadFrom(resp.Body)
	body = buf.Bytes()

	return resp.StatusCode, body, nil
}

var filled = false

func routineManager(finishedInput chan struct{}, threads int, indexChan chan string, configChan chan string, writeChan chan []byte, wg *sync.WaitGroup) {
	q := make(chan struct{}, threads)
	lolgroup := sync.WaitGroup{}
	for {
		//fmt.Println("starting..", len(q), filled, len(indexChan), len(configChan), len(writeChan))
		select {
		case q <- struct{}{}:
			lolgroup.Add(1)
			go taskWorker(&lolgroup, indexChan, configChan, writeChan, q)
		case _, ok := <-finishedInput:
			if !ok {
				filled = true
				finishedInput = nil
			}
		}
		if filled && len(indexChan) == 0 && len(configChan) == 0 && len(writeChan) == 0 {
			fmt.Println("WAITING")
			lolgroup.Wait()
			fmt.Println("BREAKING")
			wg.Done()
			return
		}
		//fmt.Println("waiting..", len(q), filled, len(indexChan), len(configChan), len(writeChan))
		//time.Sleep(time.Second * 2)
	}
}

func taskWorker(lolgroup *sync.WaitGroup, indexChan chan string, configChan chan string, writeChan chan []byte, finishedIndicator chan struct{}) {
	defer func() {
		_ = <-finishedIndicator
		lolgroup.Done()
	}()
	select {
	case t := <-indexChan:
		//send a HEAD request for the index file. Add to config queue if successful
		code, le, err := headder(t)
		if err != nil {
			return
		}

		if le < 10 || code != 200 {
			return
		}
		//content length is over 10, status code is 200, likely a good result.

		configChan <- t

	case t := <-configChan:
		//try to get the config file, write to disk if successful
		code, body, err := getter(t)

		if err != nil {
			return
		}

		if code != 200 {
			return
		}

		if strings.Contains(string(body), "[core]") {
			if strings.Contains(strings.ToLower(string(body)), "<!doctype") {
				return //if we got some weird html through for some reason
			}
			//we got a vuln
			writeChan <- append([]byte("\n~$$ Git found: "+t+" $$~\n"), body...)
		}
	}
}

func RoutineManager(s *State, ScanChan chan Host, DirbustChan chan Host, ScreenshotChan chan Host, wg *sync.WaitGroup) {
	defer wg.Done()
	targetHost := make(TargetHost, s.Threads)
	var err error
	for {
		select {
		case host := <-s.Targets:
			if !s.Scan {
				// We're not supposed to scan, so let's pump it into the output chan!
				ScanChan <- host
				break
			}
			go targetHost.ConnectHost(s, host, ScanChan)
		case host := <-ScanChan:
			var fuggoff bool
			// Do dirbusting
			if !s.Dirbust {
				// We're not supposed to dirbust, so let's pump it into the output chan!
				DirbustChan <- host
				break
			}
			if !s.URLProvided && !host.PrefetchDoneCheck(s.PrefetchedHosts) {
				host, err = Prefetch(host, s)
				if err != nil {
					fuggoff = true
				}
				if host.Protocol == "" {
					fuggoff = true
				}
				s.PrefetchedHosts[host.PrefetchHash()] = true
			}
			if s.Soft404Detection && !host.Soft404DoneCheck(s.Soft404edHosts) {
				randURL := fmt.Sprintf("%v://%v:%v/%v", host.Protocol, host.HostAddr, host.Port, RandString(16))
				fmt.Printf("Soft404 checking [%v]\n", randURL)
				randResp, err := cl.Get(randURL)
				if err != nil {
					fuggoff = true
					break
					// panic(err)
				}
				data, err := ioutil.ReadAll(randResp.Body)
				if err != nil {
					// panic(err)
					fuggoff = true
					break
				}
				randResp.Body.Close()
				host.Soft404RandomURL = randURL
				host.Soft404RandomPageContents = strings.Split(string(data), " ")
				s.Soft404edHosts[host.Soft404Hash()] = true
			}
			if !fuggoff {
				for path, _ := range s.Paths.Set {
					// fmt.Printf("HTTP GET to [%v://%v:%v/%v]\n", host.Protocol, host.HostAddr, host.Port, host.Path)
					go targetHost.HTTPGetter(host, s.Debug, s.Jitter, s.Soft404Detection, s.StatusCodesIgn, s.Ratio, path, DirbustChan)
				}

			}
		case host := <-DirbustChan:
			// Do Screenshotting
			if !s.Screenshot {
				// We're not supposed to screenshot, so let's pump it into the output chan!
				ScreenshotChan <- host
				break
			}
			go targetHost.ScreenshotHost(s, host, ScreenshotChan)
		}
		if s.Targets == nil && ScanChan == nil && DirbustChan == nil {
			return
		}
	}

}
