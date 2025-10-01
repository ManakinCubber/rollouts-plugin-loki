package plugin

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/argoproj/argo-rollouts/metricproviders/plugin/rpc"
	"github.com/argoproj/argo-rollouts/utils/evaluate"
	"io"
	"k8s.io/utils/env"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/argoproj/argo-rollouts/pkg/apis/rollouts/v1alpha1"
	log "github.com/sirupsen/logrus"

	goPlugin "github.com/hashicorp/go-plugin"
)

const (
	BasicAuthCredentials = "myuser:mypassword"
)

var testHandshake = goPlugin.HandshakeConfig{
	ProtocolVersion:  1,
	MagicCookieKey:   "ARGO_ROLLOUTS_RPC_PLUGIN",
	MagicCookieValue: "metrics",
}

func pluginClient(t *testing.T) (rpc.MetricProviderPlugin, goPlugin.ClientProtocol, func(), chan struct{}) {
	logCtx := *log.WithFields(log.Fields{"plugin-test": "loki"})
	ctx, cancel := context.WithCancel(context.Background())

	rpcPluginImp := &RpcPlugin{
		LogCtx: logCtx,
	}

	// pluginMap is the map of plugins we can dispense.
	var pluginMap = map[string]goPlugin.Plugin{
		"RpcMetricProviderPlugin": &rpc.RpcMetricProviderPlugin{Impl: rpcPluginImp},
	}

	ch := make(chan *goPlugin.ReattachConfig, 1)
	closeCh := make(chan struct{})
	go goPlugin.Serve(&goPlugin.ServeConfig{
		HandshakeConfig: testHandshake,
		Plugins:         pluginMap,
		Test: &goPlugin.ServeTestConfig{
			Context:          ctx,
			ReattachConfigCh: ch,
			CloseCh:          closeCh,
		},
	})

	// We should get a config
	var config *goPlugin.ReattachConfig
	select {
	case config = <-ch:
	case <-time.After(2000 * time.Millisecond):
		t.Fatal("should've received reattach")
	}
	if config == nil {
		t.Fatal("config should not be nil")
	}

	// Connect!
	c := goPlugin.NewClient(&goPlugin.ClientConfig{
		Cmd:             nil,
		HandshakeConfig: testHandshake,
		Plugins:         pluginMap,
		Reattach:        config,
	})
	client, err := c.Client()
	if err != nil {
		t.Fatalf("err: %s", err)
	}

	// Request the plugin
	raw, err := client.Dispense("RpcMetricProviderPlugin")
	if err != nil {
		t.Fail()
	}

	plugin, ok := raw.(rpc.MetricProviderPlugin)
	if !ok {
		t.Fail()
	}

	return plugin, client, cancel, closeCh
}

// This is just an example of how to test a plugin.
func TestRunSuccessfully(t *testing.T) {
	plugin, _, cancel, closeCh := pluginClient(t)
	defer cancel()
	lokiServer := mockLokiServer("")
	defer lokiServer.Close()

	err := plugin.InitPlugin()
	if err.Error() != "" {
		t.Fail()
	}

	msg := map[string]interface{}{
		"address":  env.GetString("LOKI_ADDRESS", lokiServer.URL),
		"username": env.GetString("LOKI_USERNAME", "myuser"),
		"password": env.GetString("LOKI_PASSWORD", "mypassword"),
		"query":    env.GetString("LOKI_QUERY", `sum(rate({cluster="test", namespace="test"} |= 'ERROR' [15m]))`),
	}

	jsonBytes, e := json.Marshal(msg)
	if e != nil {
		t.Fail()
	}

	jsonStr := string(jsonBytes)

	runMeasurement := plugin.Run(&v1alpha1.AnalysisRun{}, v1alpha1.Metric{
		Provider: v1alpha1.MetricProvider{
			Plugin: map[string]json.RawMessage{"ManakinCubber/rollouts-plugin-loki": json.RawMessage(jsonStr)},
		},
		SuccessCondition: "result[len(result)-1] <= 1",
	})
	fmt.Println(runMeasurement)
	if string(runMeasurement.Phase) != "Successful" {
		t.Fail()
	}

	cancel()
	<-closeCh

	config := Config{
		Address:  "https://logs-prod-012.grafana.net",
		Username: "994678",
		Password: "glc_eyJvIjoiMTIyMTE5MCIsIm4iOiJhcmdvLXJvbGxvdXQtdGlpbWUtYXJnby1yb2xsb3V0LXRpaW1lIiwiayI6Ing1U2o3MDlsU3o3NzQ3S2F6WWg1N05uWiIsIm0iOnsiciI6InByb2QtZXUtd2VzdC0yIn19",
		Query:    "sum(rate({cluster=\"tiime-preprod\", namespace=\"chronos-development\"} |= `ERROR` [30m]))",
	}

	client := http.Client{Timeout: time.Duration(10) * time.Second}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	config.Address = config.Address + ApiQuery
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, config.Address, nil)
	params := url.Values{}
	params.Add("query", config.Query)
	req.URL.RawQuery = params.Encode()

	if config.Username != "" && config.Password != "" {
		req.SetBasicAuth(config.Username, config.Password)
	}

	res, _ := client.Do(req)
	response := QueryResponse{}
	body, _ := io.ReadAll(res.Body)
	json.Unmarshal(body, &response)
	defer res.Body.Close()

	metric := v1alpha1.Metric{}
	newValue, newStatus, _ := processResponse(metric, response)
	fmt.Println(newValue)
	fmt.Println(newStatus)
	fmt.Println(response)
}

func processResponse(metric v1alpha1.Metric, response QueryResponse) (string, v1alpha1.AnalysisPhase, error) {
	logCtx := log.Entry{}
	switch response.Data.ResultType {
	case "vector":
		results := make([]float64, 0, len(response.Data.Result))
		valueStr := "["
		for _, s := range response.Data.Result {
			if s.Value != nil {
				for index, v := range s.Value {
					if index > 0 {
						itemValue := rawToString(v)
						log.Infof("Processing result: %s", itemValue)
						valueFloat, err := strconv.ParseFloat(itemValue, 64)
						if err != nil {
							return "", v1alpha1.AnalysisPhaseError, err
						}
						valueStr = valueStr + itemValue + ","
						results = append(results, valueFloat)
					}
				}
			}
		}
		// if we appended to the string, we should remove the last comma on the string
		if len(valueStr) > 1 {
			valueStr = valueStr[:len(valueStr)-1]
		}
		valueStr = valueStr + "]"
		newStatus, err := evaluate.EvaluateResult(results, metric, logCtx)
		return valueStr, newStatus, err
	default:
		return "", v1alpha1.AnalysisPhaseError, fmt.Errorf("Loki log type not supported ")
	}
}

func mockLokiServer(expectedAuthorizationHeader string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {

		log.StandardLogger().Infof("Received loki query")

		authorizationHeader := r.Header.Get("Authorization")
		// Reject call if we don't find the expected oauth token
		if (expectedAuthorizationHeader != "" && ("Bearer "+expectedAuthorizationHeader) != authorizationHeader) || (expectedAuthorizationHeader == "" && ("Basic "+base64.StdEncoding.EncodeToString([]byte(BasicAuthCredentials))) != authorizationHeader) {

			log.StandardLogger().Infof("Authorization header not as expected, rejecting")
			sc := http.StatusUnauthorized
			w.WriteHeader(sc)

		} else {
			log.StandardLogger().Infof("Authorization header as expected, continuing")
			lokiResponse := `{"status": "success", "data": {"resultType": "vector", "result": [{"metric": {}, "value": [1758802217.059, "0.07111111111111111"]}]}}`

			sc := http.StatusOK
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(sc)
			w.Write([]byte(lokiResponse))
		}
	}))
}
