package proxy

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strings"

	"github.com/magisterquis/connectproxy"
	netproxy "golang.org/x/net/proxy"
	authenticationv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/httpstream"
	utilnet "k8s.io/apimachinery/pkg/util/net"
	"k8s.io/apimachinery/pkg/util/proxy"
	"k8s.io/apiserver/pkg/authentication/user"
	"k8s.io/apiserver/pkg/endpoints/handlers/responsewriters"
	"k8s.io/apiserver/pkg/endpoints/request"
	"k8s.io/apiserver/pkg/registry/rest"
	registryrest "k8s.io/apiserver/pkg/registry/rest"
	clientgorest "k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
	"k8s.io/client-go/transport"
	"k8s.io/klog/v2"

	clusterapis "github.com/karmada-io/karmada/pkg/apis/cluster"
	"github.com/karmada-io/karmada/pkg/util/lifted"
)

// ConnectCluster returns a handler for proxy cluster.
func ConnectCluster(
	ctx context.Context,
	cluster *clusterapis.Cluster,
	proxyPath string,
	secretGetter func(context.Context, string, string) (*corev1.Secret, error),
	responder rest.Responder,
) (http.Handler, error) {
	location, proxyTransport, err := Location(cluster)
	if err != nil {
		return nil, err
	}

	location.Path = path.Join(location.Path, proxyPath)

	if cluster.Spec.ImpersonatorSecretRef == nil {
		return nil, fmt.Errorf("the impersonatorSecretRef of cluster %s is nil", cluster.Name)
	}

	proxyRequestInfo := lifted.NewRequestInfo(&http.Request{URL: &url.URL{Path: proxyPath}})
	isExecCmd := proxyRequestInfo.Subresource == "exec"

	secret, err := secretGetter(ctx, cluster.Spec.ImpersonatorSecretRef.Namespace, cluster.Spec.ImpersonatorSecretRef.Name)
	if err != nil {
		return nil, err
	}

	impersonateToken, err := getImpersonateToken(cluster.Name, secret)
	if err != nil {
		return nil, fmt.Errorf("failed to get impresonateToken for cluster %s: %v", cluster.Name, err)
	}

	return newProxyHandlerNew(location, proxyTransport, cluster, impersonateToken, isExecCmd, responder)
}

func newProxyHandlerNew(
	location *url.URL,
	proxyTransport http.RoundTripper,
	cluster *clusterapis.Cluster,
	impersonateToken string,
	isExecCmd bool,
	responder rest.Responder,
) (http.Handler, error) {
	if isExecCmd {
		return http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			requester, exist := request.UserFrom(req.Context())
			if !exist {
				responsewriters.InternalError(rw, req, errors.New("no user found for request"))
				return
			}

			impersonateGroups := make([]string, 0, len(requester.GetGroups()))
			for _, group := range requester.GetGroups() {
				if !skipGroup(group) {
					impersonateGroups = append(impersonateGroups, group)
				}
			}

			var proxyURL *url.URL
			if cluster.Spec.ProxyURL != "" {
				proxyURL, _ = url.Parse(cluster.Spec.ProxyURL)
			}
			cfg := &clientgorest.Config{
				Host:        cluster.Spec.APIEndpoint,
				BearerToken: impersonateToken,
				Impersonate: clientgorest.ImpersonationConfig{
					UserName: requester.GetName(),
					Groups:   impersonateGroups,
				},
				TLSClientConfig: clientgorest.TLSClientConfig{Insecure: true},
				Proxy:           http.ProxyURL(proxyURL),
			}

			// Retain RawQuery in location because upgrading the request will use it.
			// See https://github.com/karmada-io/karmada/issues/1618#issuecomment-1103793290 for more info.
			location.RawQuery = req.URL.RawQuery

			exec, err := remotecommand.NewSPDYExecutor(cfg, "POST", location)
			if err != nil {
				klog.Errorf("jw:%v", err)
				return
			}
			in := &bytes.Buffer{}
			out := &bytes.Buffer{}
			errOut := &bytes.Buffer{}
			err = exec.StreamWithContext(context.Background(), remotecommand.StreamOptions{
				Stdin:             in,
				Stdout:            out,
				Stderr:            errOut,
				Tty:               false,
				TerminalSizeQueue: nil,
			})
			if err != nil {
				klog.Errorf("jw:%v", err)
				return
			}

			outStr := out.String()
			klog.InfoS("[debug: jw]", "in", in.String(), "out", outStr, "outErr", errOut.String())

			rw.Header().Set("X-Stream-Protocol-Version", "v4.channel.k8s.io")
			rw.Header().Set("Connection", "Upgrade")
			rw.Header().Set("Upgrade", "SPDY/3.1")
			rw.WriteHeader(http.StatusOK)
			_, _ = rw.Write([]byte(outStr))
			rw.(http.Flusher).Flush()

			//proxyTransport, err := clientgorest.TransportFor(cfg)
			//if err != nil {
			//	klog.Errorf("jw:%v", err)
			//	return
			//}
			//upgradeTransport, err := makeUpgradeTransport(cfg, proxyURL)
			//if err != nil {
			//	klog.Errorf("jw:%v", err)
			//	return
			//}
			//
			//handler := proxy.NewUpgradeAwareHandler(location, proxyTransport, false, false, proxy.NewErrorResponder(responder))
			//handler.UpgradeTransport = upgradeTransport
			//handler.UseRequestLocation = true
			//
			//handler.ServeHTTP(rw, req)
		}), nil
	}
	return http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		requester, exist := request.UserFrom(req.Context())
		if !exist {
			responsewriters.InternalError(rw, req, errors.New("no user found for request"))
			return
		}

		req.Header.Set(authenticationv1.ImpersonateUserHeader, requester.GetName())
		for _, group := range requester.GetGroups() {
			if !skipGroup(group) {
				req.Header.Add(authenticationv1.ImpersonateGroupHeader, group)
			}
		}
		req.Header.Set("Authorization", fmt.Sprintf("bearer %s", impersonateToken))

		// Retain RawQuery in location because upgrading the request will use it.
		// See https://github.com/karmada-io/karmada/issues/1618#issuecomment-1103793290 for more info.
		location.RawQuery = req.URL.RawQuery

		handler := proxy.NewUpgradeAwareHandler(location, proxyTransport, false, false, proxy.NewErrorResponder(responder))
		handler.ServeHTTP(rw, req)
	}), nil
}

func makeUpgradeTransport(config *clientgorest.Config, proxyURL *url.URL) (proxy.UpgradeRequestRoundTripper, error) {
	//transportConfig, err := config.TransportConfig()
	//if err != nil {
	//	return nil, err
	//}
	//tlsConfig, err := transport.TLSConfigFor(transportConfig)
	//if err != nil {
	//	return nil, err
	//}
	//rt := utilnet.SetOldTransportDefaults(&http.Transport{
	//	TLSClientConfig: tlsConfig,
	//	DialContext: (&net.Dialer{
	//		Timeout:   30 * time.Second,
	//		KeepAlive: 30 * time.Second,
	//	}).DialContext,
	//	Proxy: config.Proxy,
	//})
	//
	//upgrader, err := transport.HTTPWrappersForConfig(transportConfig, rt)
	//if err != nil {
	//	return nil, err
	//}
	//return proxy.NewUpgradeRequestRoundTripper(rt, upgrader), nil

	tlsConfig, err := clientgorest.TLSConfigFor(config)
	if err != nil {
		return nil, err
	}

	d, err := connectproxy.New(proxyURL, netproxy.Direct)
	if nil != err {
		return nil, err
	}

	upgradeRoundTripper := utilnet.SetOldTransportDefaults(&http.Transport{
		TLSClientConfig: tlsConfig,
		Dial:            d.Dial,
		Proxy:           config.Proxy,
	})

	wapper, err := clientgorest.HTTPWrappersForConfig(config, upgradeRoundTripper)
	if err != nil {
		return nil, err
	}
	return proxy.NewUpgradeRequestRoundTripper(wapper, upgradeRoundTripper), nil
}

// NewThrottledUpgradeAwareProxyHandler creates a new proxy handler with a default flush interval. Responder is required for returning
// errors to the caller.
func NewThrottledUpgradeAwareProxyHandler(location *url.URL, transport http.RoundTripper, wrapTransport, upgradeRequired bool, responder registryrest.Responder) *proxy.UpgradeAwareHandler {
	return proxy.NewUpgradeAwareHandler(location, transport, wrapTransport, upgradeRequired, proxy.NewErrorResponder(responder))
}

// Location returns a URL to which one can send traffic for the specified cluster.
func Location(cluster *clusterapis.Cluster) (*url.URL, http.RoundTripper, error) {
	location, err := constructLocation(cluster)
	if err != nil {
		return nil, nil, err
	}

	proxyTransport, err := createProxyTransport(cluster)
	if err != nil {
		return nil, nil, err
	}

	return location, proxyTransport, nil
}

func constructLocation(cluster *clusterapis.Cluster) (*url.URL, error) {
	apiEndpoint := cluster.Spec.APIEndpoint
	if apiEndpoint == "" {
		return nil, fmt.Errorf("API endpoint of cluster %s should not be empty", cluster.GetName())
	}

	uri, err := url.Parse(apiEndpoint)
	if err != nil {
		return nil, fmt.Errorf("failed to parse api endpoint %s: %v", apiEndpoint, err)
	}
	return uri, nil
}

func createProxyTransport(cluster *clusterapis.Cluster) (*http.Transport, error) {
	var proxyDialerFn utilnet.DialFunc
	proxyTLSClientConfig := &tls.Config{InsecureSkipVerify: true} // #nosec
	trans := utilnet.SetTransportDefaults(&http.Transport{
		DialContext:     proxyDialerFn,
		TLSClientConfig: proxyTLSClientConfig,
	})

	if proxyURL := cluster.Spec.ProxyURL; proxyURL != "" {
		u, err := url.Parse(proxyURL)
		if err != nil {
			return nil, fmt.Errorf("failed to parse url of proxy url %s: %v", proxyURL, err)
		}
		trans.Proxy = http.ProxyURL(u)
	}

	if len(cluster.Spec.ProxyHeader) != 0 {
		trans.ProxyConnectHeader = parseProxyHeaders(cluster.Spec.ProxyHeader)
	}

	return trans, nil
}

func parseProxyHeaders(proxyHeaders map[string]string) http.Header {
	header := http.Header{}
	for headerKey, headerValues := range proxyHeaders {
		values := strings.Split(headerValues, ",")
		header[headerKey] = values
	}
	return header
}

func getImpersonateToken(clusterName string, secret *corev1.Secret) (string, error) {
	token, found := secret.Data[clusterapis.SecretTokenKey]
	if !found {
		return "", fmt.Errorf("the impresonate token of cluster %s is empty", clusterName)
	}
	return string(token), nil
}

func newProxyHandler(location *url.URL, proxyRT http.RoundTripper, impersonateToken string, responder registryrest.Responder) (http.Handler, error) {
	return http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		requester, exist := request.UserFrom(req.Context())
		if !exist {
			responsewriters.InternalError(rw, req, errors.New("no user found for request"))
			return
		}
		req.Header.Set(authenticationv1.ImpersonateUserHeader, requester.GetName())
		for _, group := range requester.GetGroups() {
			if !skipGroup(group) {
				req.Header.Add(authenticationv1.ImpersonateGroupHeader, group)
			}
		}

		upgrade := httpstream.IsUpgradeRequest(req)
		req.Header.Set("Authorization", fmt.Sprintf("bearer %s", impersonateToken))

		// Retain RawQuery in location because upgrading the request will use it.
		// See https://github.com/karmada-io/karmada/issues/1618#issuecomment-1103793290 for more info.
		location.RawQuery = req.URL.RawQuery

		proxyRoundTripper := transport.NewAuthProxyRoundTripper(requester.GetName(), requester.GetGroups(), requester.GetExtra(), proxyRT)

		newReq := req.WithContext(req.Context())
		newReq.Header = utilnet.CloneHeader(req.Header)
		newReq.URL = location
		newReq.Host = location.Host

		if upgrade {
			transport.SetAuthProxyHeaders(newReq, requester.GetName(), requester.GetGroups(), requester.GetExtra())
		}

		handler := NewThrottledUpgradeAwareProxyHandler(location, proxyRoundTripper, true, upgrade, responder)
		handler.ServeHTTP(rw, newReq)
	}), nil
}

func skipGroup(group string) bool {
	switch group {
	case user.AllAuthenticated, user.AllUnauthenticated:
		return true
	default:
		return false
	}
}
