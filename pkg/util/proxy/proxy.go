package proxy

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	clusterapis "github.com/karmada-io/karmada/pkg/apis/cluster"
	authenticationv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	utilnet "k8s.io/apimachinery/pkg/util/net"
	"k8s.io/apimachinery/pkg/util/proxy"
	"k8s.io/apiserver/pkg/authentication/user"
	"k8s.io/apiserver/pkg/endpoints/handlers/responsewriters"
	"k8s.io/apiserver/pkg/endpoints/request"
	registryrest "k8s.io/apiserver/pkg/registry/rest"
	clientgorest "k8s.io/client-go/rest"
)

// ConnectCluster returns a handler for proxy cluster.
func ConnectCluster(
	ctx context.Context,
	cluster *clusterapis.Cluster, proxyPath string,
	secretGetter func(context.Context, string, string) (*corev1.Secret, error),
	responder registryrest.Responder,
) (http.Handler, error) {
	if cluster.Spec.ImpersonatorSecretRef == nil {
		return nil, fmt.Errorf("the impersonatorSecretRef of cluster %s is nil", cluster.Name)
	}

	secret, err := secretGetter(ctx, cluster.Spec.ImpersonatorSecretRef.Namespace, cluster.Spec.ImpersonatorSecretRef.Name)
	if err != nil {
		return nil, err
	}

	impersonateToken, err := getImpersonateToken(cluster.Name, secret)
	if err != nil {
		return nil, fmt.Errorf("failed to get impresonateToken for cluster %s: %v", cluster.Name, err)
	}

	location, proxyTransport, err := Location(cluster)
	if err != nil {
		return nil, err
	}
	location.Path = path.Join(location.Path, proxyPath)
	return newProxyHandler(location, proxyTransport, cluster, impersonateToken, responder)
}

func newProxyHandler(
	location *url.URL,
	proxyTransport http.RoundTripper,
	cluster *clusterapis.Cluster,
	impersonateToken string,
	responder registryrest.Responder,
) (http.Handler, error) {
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

		var proxyURL *url.URL
		if proxyURLStr := cluster.Spec.ProxyURL; proxyURLStr != "" {
			proxyURL, _ = url.Parse(proxyURLStr)
		}
		cfg := &clientgorest.Config{
			Host:        cluster.Spec.APIEndpoint,
			BearerToken: impersonateToken,
			Impersonate: clientgorest.ImpersonationConfig{
				UserName: requester.GetName(),
				Groups:   requester.GetGroups(),
			},
			TLSClientConfig: clientgorest.TLSClientConfig{Insecure: true},
			Proxy:           http.ProxyURL(proxyURL),
		}
		// Retain RawQuery in location because upgrading the request will use it.
		// See https://github.com/karmada-io/karmada/issues/1618#issuecomment-1103793290 for more info.
		location.RawQuery = req.URL.RawQuery

		tlsConfig, err := clientgorest.TLSConfigFor(cfg)
		if err != nil {
			return
		}

		header := map[string][]string{}
		for k, vs := range cluster.Spec.ProxyHeader {
			v := strings.Split(vs, ",")
			header[k] = v
		}
		upgradeRoundTripper := NewNewUpgradeDialerWithConfig(UpgradeDialerWithConfig{
			TLS:        tlsConfig,
			Proxier:    http.ProxyURL(proxyURL),
			PingPeriod: time.Second * 5,
			Header:     header,
		})

		handler := NewUpgradeAwareHandler(location, proxyTransport, false, false, NewErrorResponder(responder))
		handler.SpdyTransport = upgradeRoundTripper
		handler.ServeHTTP(rw, req)
	}), nil
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

func skipGroup(group string) bool {
	switch group {
	case user.AllAuthenticated, user.AllUnauthenticated:
		return true
	default:
		return false
	}
}
