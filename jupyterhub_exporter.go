package main

import (
	"crypto/tls"
	"encoding/json"
	"flag"
	"io/ioutil"
	"log"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// ResponseJSON is struct of Jupyterhub response for /hub/api/users
type ResponseJSON []struct {
	Name         string `json:"name"`
	Server       string `json:"server"`
	LastActivity string `json:"last_activity"`
}

var (
	apiHost  = flag.String("host", "https://localhost/hub/api", "API host")
	willStop = flag.Bool("stop", true, "stop single server")
	apiToken = flag.String("token", "", "jupyterhub token (admin)")
	waitHour = flag.Int64("hours", 24, "hours to wait for stop server")
)

const (
	namespace   = "jupyterhub"
	metricsPath = "/metrics"
	dateLayout  = "2006-01-02T15:04:05.000000Z"
)

type myCollector struct{}

var (
	activeUserDesc = prometheus.NewDesc(
		"active_user",
		"Current active users.",
		[]string{"userName"}, nil,
	)
)

// APIRequest is to get response for api request with http-headers
func APIRequest(url string, method string, headers map[string]string) (result []byte, err error) {
	customTransport := &(*http.DefaultTransport.(*http.Transport)) // make shallow copy
	customTransport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}

	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		return
	}

	for key, value := range headers {
		req.Header.Set(key, value)
	}

	client := &http.Client{Transport: customTransport}
	res, err := client.Do(req)
	if err != nil {
		return
	}
	defer res.Body.Close()

	result, err = ioutil.ReadAll(res.Body)
	return
}

func (cc myCollector) Describe(ch chan<- *prometheus.Desc) {
	prometheus.DescribeByCollect(cc, ch)
}

func StopSingleServer(username string) {
	headers := map[string]string{
		"Authorization": "token " + *apiToken,
	}
	url := *apiHost + "/users/" + username + "/server"
	_, apiErr := APIRequest(url, "DELETE", headers)

	if apiErr != nil {
		log.Println(apiErr)
		return
	}
	log.Println("stopped " + username + "'s server")
	return
}

func (cc *myCollector) GetActiveUser() (
	activeUsers map[string]int64,
) {
	headers := map[string]string{
		"Authorization": "token " + *apiToken,
	}

	resBody, apiErr := APIRequest(*apiHost+"/users", "GET", headers)

	if apiErr != nil {
		log.Println(apiErr)
		return
	}

	var resJSON = ResponseJSON{}
	err := json.Unmarshal(resBody, &resJSON)

	activeUsers = map[string]int64{}

	if err == nil {
		for _, user := range resJSON {
			if user.Server != "" {
				t, _ := time.Parse(dateLayout, user.LastActivity)
				lastTimestamp := t.UnixNano()
				activeUsers[user.Name] = lastTimestamp
			}
		}
	} else {
		log.Println(err)
	}

	return
}

func (cc myCollector) Collect(ch chan<- prometheus.Metric) {
	activeUsers := cc.GetActiveUser()
	nowTimestamp := time.Now().UnixNano()

	for userName, lastActivity := range activeUsers {
		isActive := nowTimestamp-lastActivity < *waitHour*60*60*1e9
		if isActive {
			ch <- prometheus.MustNewConstMetric(
				activeUserDesc,
				prometheus.UntypedValue,
				float64(lastActivity),
				userName,
			)
		} else {
			StopSingleServer(userName)
		}
	}
}

func main() {
	flag.Parse()

	reg := prometheus.NewPedanticRegistry()
	cc := myCollector{}
	prometheus.WrapRegistererWithPrefix(namespace+"_", reg).MustRegister(cc)

	http.Handle(metricsPath, promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<html>
			<head><title>Jupyterhub Exporter</title></head>
			<body>
			<h1>Jupyterhub Exporter</h1>
			<h2>v1.1</h2>
			<p><a href="` + metricsPath + `">Metrics</a></p>
			</body>
			</html>`))
	})
	log.Println("start server")
	log.Fatal(http.ListenAndServe(":9225", nil))
}
