package integration

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const adminToken = testAdminHeaderValue

func TestDockerComposeAllAPIs(t *testing.T) {
	if os.Getenv("E2E_DOCKER") != "1" {
		t.Skip("set E2E_DOCKER=1 to run docker end-to-end tests")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker binary is not available")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()

	env := composeTestEnv{
		repoRoot: mustRepoRoot(t),
	}
	env.prepare(ctx, t)
	defer env.teardown(ctx, t)

	baseURL := fmt.Sprintf("http://127.0.0.1:%d", env.appPort)
	waitForHealth(t, baseURL+"/health", 90*time.Second)

	state := &apiState{}

	t.Run("auth", func(t *testing.T) {
		env.requestStatus(t, http.MethodGet, "/api/users", "", nil, http.StatusUnauthorized)
	})
	t.Run("users-clients-and-inbounds", func(t *testing.T) {
		exerciseUsersClientsAndInbounds(t, &env, state)
	})
	t.Run("user-lifecycle-and-token", func(t *testing.T) {
		exerciseUserLifecycleAndToken(t, &env, state)
	})
	t.Run("user-routes", func(t *testing.T) {
		exerciseUserRoutes(t, &env, state)
	})
	t.Run("global-routes", func(t *testing.T) {
		exerciseGlobalRoutes(t, &env)
	})
	t.Run("balancer-config", func(t *testing.T) {
		exerciseBalancerConfig(t, &env)
	})
	t.Run("sync-trigger", func(t *testing.T) {
		env.requestStatus(t, http.MethodPost, "/api/sync", adminToken, nil, http.StatusOK)
	})
	t.Run("subscriptions", func(t *testing.T) {
		exerciseSubscriptions(t, &env, state)
	})
}

type apiState struct {
	aliceID    int64
	aliceToken string
	inboundID  int64
	inboundTag string
}

func exerciseUsersClientsAndInbounds(t *testing.T, env *composeTestEnv, st *apiState) {
	usersBody := env.requestStatus(t, http.MethodGet, "/api/users", adminToken, nil, http.StatusOK)
	users := decodeUsers(t, usersBody)
	if len(users) < 2 {
		t.Fatalf("expected at least 2 synced users, got %d", len(users))
	}
	alice, ok := findUser(users, "alice@example.com")
	if !ok {
		t.Fatalf("alice@example.com not found in users: %+v", users)
	}
	st.aliceID = alice.ID
	st.aliceToken = alice.Token

	env.requestStatus(t, http.MethodGet, fmt.Sprintf("/api/users/%d", st.aliceID), adminToken, nil, http.StatusOK)

	clientsBody := env.requestStatus(t, http.MethodGet, fmt.Sprintf("/api/users/%d/clients", st.aliceID), adminToken, nil, http.StatusOK)
	var clients []map[string]interface{}
	if err := json.Unmarshal(clientsBody, &clients); err != nil {
		t.Fatalf("decode clients: %v body=%s", err, string(clientsBody))
	}
	if len(clients) == 0 {
		t.Fatal("expected at least one client for alice")
	}
	st.inboundID = int64FromAny(t, clients[0]["inbound_id"])

	inboundsBody := env.requestStatus(t, http.MethodGet, "/api/inbounds", adminToken, nil, http.StatusOK)
	var inbounds []map[string]interface{}
	if err := json.Unmarshal(inboundsBody, &inbounds); err != nil {
		t.Fatalf("decode inbounds: %v body=%s", err, string(inboundsBody))
	}
	if len(inbounds) == 0 {
		t.Fatal("expected at least one inbound")
	}
	st.inboundTag, _ = inbounds[0]["tag"].(string)
	if strings.TrimSpace(st.inboundTag) == "" {
		t.Fatalf("invalid inbound tag in response: %+v", inbounds[0])
	}

	env.requestStatus(t, http.MethodPut, fmt.Sprintf("/api/users/%d/clients/%d/disable", st.aliceID, st.inboundID), adminToken, nil, http.StatusOK)
	env.requestStatus(t, http.MethodPut, fmt.Sprintf("/api/users/%d/clients/%d/enable", st.aliceID, st.inboundID), adminToken, nil, http.StatusOK)
}

func exerciseUserLifecycleAndToken(t *testing.T, env *composeTestEnv, st *apiState) {
	if st.aliceID == 0 {
		t.Fatal("missing alice id from previous setup")
	}
	createBody := env.requestStatus(t, http.MethodPost, "/api/users", adminToken, []byte(`{"username":"charlie-e2e"}`), http.StatusOK)
	var created struct {
		User struct {
			ID int64 `json:"id"`
		} `json:"user"`
	}
	if err := json.Unmarshal(createBody, &created); err != nil {
		t.Fatalf("decode create user: %v body=%s", err, string(createBody))
	}
	if created.User.ID <= 0 {
		t.Fatalf("invalid created user id: %+v", created)
	}
	env.requestStatus(t, http.MethodDelete, fmt.Sprintf("/api/users/%d", created.User.ID), adminToken, nil, http.StatusOK)

	env.requestStatus(t, http.MethodPut, fmt.Sprintf("/api/users/%d/disable", st.aliceID), adminToken, nil, http.StatusOK)
	env.requestStatus(t, http.MethodPut, fmt.Sprintf("/api/users/%d/enable", st.aliceID), adminToken, nil, http.StatusOK)

	tokenBody := env.requestStatus(t, http.MethodPost, fmt.Sprintf("/api/users/%d/token", st.aliceID), adminToken, nil, http.StatusOK)
	var tokenResp struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(tokenBody, &tokenResp); err != nil {
		t.Fatalf("decode token response: %v body=%s", err, string(tokenBody))
	}
	if strings.TrimSpace(tokenResp.Token) == "" {
		t.Fatal("regenerated token is empty")
	}
	st.aliceToken = tokenResp.Token
}

func exerciseUserRoutes(t *testing.T, env *composeTestEnv, st *apiState) {
	if st.aliceID == 0 {
		t.Fatal("missing alice id from previous setup")
	}
	routesBody := env.requestStatus(t, http.MethodGet, fmt.Sprintf("/api/users/%d/routes", st.aliceID), adminToken, nil, http.StatusOK)
	var routesResp map[string]interface{}
	if err := json.Unmarshal(routesBody, &routesResp); err != nil {
		t.Fatalf("decode routes response: %v body=%s", err, string(routesBody))
	}

	addRouteBody := env.requestStatus(t, http.MethodPost, fmt.Sprintf("/api/users/%d/routes", st.aliceID), adminToken, []byte(`{"outboundTag":"direct","domain":["example.com"]}`), http.StatusOK)
	var addRouteResp map[string]interface{}
	if err := json.Unmarshal(addRouteBody, &addRouteResp); err != nil {
		t.Fatalf("decode add route: %v body=%s", err, string(addRouteBody))
	}
	routeID := routeIDFromResponse(t, addRouteResp)
	routeIndex := intFromAny(t, addRouteResp["index"])

	env.requestStatus(
		t,
		http.MethodPut,
		fmt.Sprintf("/api/users/%d/routes/%d", st.aliceID, routeIndex),
		adminToken,
		[]byte(fmt.Sprintf(`{"id":"%s","outboundTag":"proxy","domain":["github.com"]}`, routeID)),
		http.StatusOK,
	)
	env.requestStatus(
		t,
		http.MethodPut,
		fmt.Sprintf("/api/users/%d/routes/id/%s", st.aliceID, routeID),
		adminToken,
		[]byte(fmt.Sprintf(`{"id":"%s","outboundTag":"block","domain":["ads.example"]}`, routeID)),
		http.StatusOK,
	)
	env.requestStatus(t, http.MethodDelete, fmt.Sprintf("/api/users/%d/routes/id/%s", st.aliceID, routeID), adminToken, nil, http.StatusOK)

	setRoutes := `{"rules":[{"outboundTag":"direct","domain":["example.org"]},{"outboundTag":"proxy","ip":["1.1.1.1"]}]}`
	setRoutesBody := env.requestStatus(t, http.MethodPut, fmt.Sprintf("/api/users/%d/routes", st.aliceID), adminToken, []byte(setRoutes), http.StatusOK)
	var setRoutesResp map[string]interface{}
	if err := json.Unmarshal(setRoutesBody, &setRoutesResp); err != nil {
		t.Fatalf("decode set routes: %v body=%s", err, string(setRoutesBody))
	}
	rules, _ := setRoutesResp["rules"].([]interface{})
	if len(rules) < 2 {
		t.Fatalf("expected 2 rules after set, got %+v", setRoutesResp)
	}
	env.requestStatus(t, http.MethodDelete, fmt.Sprintf("/api/users/%d/routes/0", st.aliceID), adminToken, nil, http.StatusOK)
}

func exerciseGlobalRoutes(t *testing.T, env *composeTestEnv) {
	env.requestStatus(t, http.MethodGet, "/api/routes/global", adminToken, nil, http.StatusOK)
	env.requestStatus(t, http.MethodPost, "/api/routes/global", adminToken, []byte(`{"outboundTag":"proxy","domain":["ru-blocked"]}`), http.StatusOK)
	env.requestStatus(t, http.MethodPut, "/api/routes/global", adminToken, []byte(`{"rules":[{"outboundTag":"direct","domain":["okko.tv"]}]}`), http.StatusOK)
	env.requestStatus(t, http.MethodDelete, "/api/routes/global", adminToken, nil, http.StatusOK)
}

func exerciseBalancerConfig(t *testing.T, env *composeTestEnv) {
	env.requestStatus(t, http.MethodGet, "/api/config/balancer", adminToken, nil, http.StatusOK)
	env.requestStatus(t, http.MethodPut, "/api/config/balancer", adminToken, []byte(`{"strategy":"random"}`), http.StatusOK)
	env.requestStatus(t, http.MethodGet, "/api/config/balancer", adminToken, nil, http.StatusOK)
	env.requestStatus(t, http.MethodPut, "/api/config/balancer", adminToken, []byte(`{"reset":true}`), http.StatusOK)
}

func exerciseSubscriptions(t *testing.T, env *composeTestEnv, st *apiState) {
	if st.aliceToken == "" || st.inboundTag == "" {
		t.Fatal("missing state for subscription checks")
	}
	env.subStatus(t, st.aliceToken, "", http.StatusOK)
	env.subStatus(t, st.aliceToken, "?format=b64", http.StatusOK)
	env.subStatus(t, st.aliceToken, "?profile=mobile", http.StatusOK)

	env.subStatus(t, st.aliceToken, "/links", http.StatusOK)
	linksTxt := string(env.subStatus(t, st.aliceToken, "/links.txt", http.StatusOK))
	if !strings.Contains(linksTxt, "vless://") {
		t.Fatalf("links.txt does not contain vless:// : %s", linksTxt)
	}

	linksB64 := strings.TrimSpace(string(env.subStatus(t, st.aliceToken, "/links.b64", http.StatusOK)))
	decoded, err := base64.StdEncoding.DecodeString(linksB64)
	if err != nil {
		t.Fatalf("decode links.b64: %v value=%s", err, linksB64)
	}
	if !strings.Contains(string(decoded), "vless://") {
		t.Fatalf("decoded links.b64 does not contain vless:// : %s", string(decoded))
	}

	env.subStatus(t, st.aliceToken, "/vless", http.StatusOK)
	env.subStatus(t, st.aliceToken, "/vless.b64", http.StatusOK)
	vlessListBody := env.subStatus(t, st.aliceToken, "/vless/list", http.StatusOK)
	var vlessList struct {
		Items []map[string]interface{} `json:"items"`
	}
	if err := json.Unmarshal(vlessListBody, &vlessList); err != nil {
		t.Fatalf("decode vless list: %v body=%s", err, string(vlessListBody))
	}
	if len(vlessList.Items) == 0 {
		t.Fatalf("vless list is empty: %s", string(vlessListBody))
	}
	vlessTag, _ := vlessList.Items[0]["tag"].(string)
	if strings.TrimSpace(vlessTag) == "" {
		t.Fatalf("empty vless tag in list: %+v", vlessList.Items[0])
	}
	env.subStatus(t, st.aliceToken, "/vless/"+vlessTag, http.StatusOK)
	env.subStatus(t, st.aliceToken, "/vless/"+vlessTag+"/b64", http.StatusOK)

	env.subStatus(t, st.aliceToken, "/protocol/vless", http.StatusOK)
	env.subStatus(t, st.aliceToken, "/protocol/vless/links.txt", http.StatusOK)
	env.subStatus(t, st.aliceToken, "/protocol/vless/links.b64", http.StatusOK)

	// Protocol-specific endpoints not present in test config should return 404.
	env.subStatus(t, st.aliceToken, "/vmess", http.StatusNotFound)
	env.subStatus(t, st.aliceToken, "/vmess.b64", http.StatusNotFound)
	env.subStatus(t, st.aliceToken, "/trojan", http.StatusNotFound)
	env.subStatus(t, st.aliceToken, "/trojan.b64", http.StatusNotFound)
	env.subStatus(t, st.aliceToken, "/ss", http.StatusNotFound)
	env.subStatus(t, st.aliceToken, "/ss.b64", http.StatusNotFound)
	env.subStatus(t, st.aliceToken, "/shadowsocks", http.StatusNotFound)
	env.subStatus(t, st.aliceToken, "/shadowsocks.b64", http.StatusNotFound)
	env.subStatus(t, "invalid-token", "", http.StatusNotFound)
}

type composeTestEnv struct {
	repoRoot      string
	tmpDir        string
	projectName   string
	appPort       int
	xrayPort      int
	appConfigPath string
	appBinPath    string
}

func (e *composeTestEnv) prepare(ctx context.Context, t *testing.T) {
	t.Helper()

	e.tmpDir = t.TempDir()
	e.projectName = fmt.Sprintf("ravene2e%d", time.Now().UnixNano())
	e.appPort = reservePort(t)
	e.xrayPort = reservePort(t)
	e.appConfigPath = filepath.Join(e.tmpDir, "config.json")
	e.appBinPath = filepath.Join(e.tmpDir, "xray-subscription")

	buildCmd := fmt.Sprintf("CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o %q .", e.appBinPath)
	if out, err := runCmd(ctx, e.repoRoot, "sh", "-c", buildCmd); err != nil {
		t.Fatalf("build app binary failed: %v\n%s", err, out)
	}

	appConfig := fmt.Sprintf(`{
  "listen_addr": ":8080",
  "server_host": "127.0.0.1",
  "config_dir": "/etc/xray/config.d",
  "db_path": "/var/lib/xray-subscription/db.sqlite",
  "sync_interval_seconds": 60,
  "base_url": "http://127.0.0.1:%d",
  "admin_token": "%s",
  "balancer_strategy": "leastPing",
  "balancer_probe_url": "https://www.gstatic.com/generate_204",
  "balancer_probe_interval": "30s"
}
`, e.appPort, adminToken)
	if err := os.WriteFile(e.appConfigPath, []byte(appConfig), 0o600); err != nil {
		t.Fatalf("write app config: %v", err)
	}

	env := e.composeEnv()
	if out, err := runCmdEnv(ctx, e.repoRoot, env, "docker", "compose", "-f", "docker-compose.test.yml", "down", "-v"); err != nil {
		t.Logf("docker compose down output: %s", out)
	}
	if out, err := runCmdEnv(ctx, e.repoRoot, env, "docker", "compose", "-f", "docker-compose.test.yml", "up", "-d"); err != nil {
		t.Fatalf("docker compose up failed: %v\n%s", err, out)
	}
}

func (e *composeTestEnv) teardown(ctx context.Context, t *testing.T) {
	t.Helper()
	env := e.composeEnv()
	if out, err := runCmdEnv(ctx, e.repoRoot, env, "docker", "compose", "-f", "docker-compose.test.yml", "down", "-v"); err != nil {
		t.Logf("docker compose down failed: %v\n%s", err, out)
	}
}

func (e *composeTestEnv) composeEnv() []string {
	xrayCfg := filepath.Join(e.repoRoot, "testdata", "xray", "config.d")
	xrayImage := strings.TrimSpace(os.Getenv("XRAY_IMAGE"))
	if xrayImage == "" {
		xrayImage = defaultXrayImage
	}
	return []string{
		"COMPOSE_PROJECT_NAME=" + e.projectName,
		"XRAY_IMAGE=" + xrayImage,
		"XRAY_CONFIG_DIR=" + xrayCfg,
		"APP_CONFIG_PATH=" + e.appConfigPath,
		"APP_BIN_PATH=" + e.appBinPath,
		"APP_HOST_PORT=" + fmt.Sprintf("%d", e.appPort),
		"XRAY_HOST_PORT=" + fmt.Sprintf("%d", e.xrayPort),
	}
}

func (e *composeTestEnv) requestStatus(t *testing.T, method, path, token string, body []byte, status int) []byte {
	t.Helper()
	url := fmt.Sprintf("http://127.0.0.1:%d%s", e.appPort, path)
	req, err := http.NewRequest(method, url, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new request %s %s: %v", method, url, err)
	}
	if token != "" {
		req.Header.Set("X-Admin-Token", token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request %s %s: %v", method, url, err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != status {
		t.Fatalf("%s %s expected %d got %d body=%s", method, url, status, resp.StatusCode, string(respBody))
	}
	return respBody
}

func (e *composeTestEnv) subStatus(t *testing.T, token, suffix string, status int) []byte {
	t.Helper()
	path := fmt.Sprintf("/sub/%s%s", token, suffix)
	return e.requestStatus(t, http.MethodGet, path, "", nil, status)
}

type userInfo struct {
	ID       int64
	Username string
	Token    string
}

func decodeUsers(t *testing.T, body []byte) []userInfo {
	t.Helper()
	var raw []struct {
		User struct {
			ID       int64  `json:"id"`
			Username string `json:"username"`
			Token    string `json:"token"`
		} `json:"user"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatalf("decode users: %v body=%s", err, string(body))
	}
	out := make([]userInfo, 0, len(raw))
	for _, u := range raw {
		out = append(out, userInfo{
			ID:       u.User.ID,
			Username: u.User.Username,
			Token:    u.User.Token,
		})
	}
	return out
}

func findUser(users []userInfo, username string) (userInfo, bool) {
	for _, u := range users {
		if u.Username == username {
			return u, true
		}
	}
	return userInfo{}, false
}

func routeIDFromResponse(t *testing.T, m map[string]interface{}) string {
	t.Helper()
	rule, ok := m["rule"].(map[string]interface{})
	if !ok {
		t.Fatalf("missing rule field in response: %+v", m)
	}
	id, _ := rule["id"].(string)
	if strings.TrimSpace(id) == "" {
		t.Fatalf("missing route id in response: %+v", m)
	}
	return id
}

func int64FromAny(t *testing.T, v interface{}) int64 {
	t.Helper()
	switch x := v.(type) {
	case float64:
		return int64(x)
	case int64:
		return x
	default:
		t.Fatalf("cannot convert to int64: %#v", v)
		return 0
	}
}

func intFromAny(t *testing.T, v interface{}) int {
	t.Helper()
	switch x := v.(type) {
	case float64:
		return int(x)
	case int:
		return x
	default:
		t.Fatalf("cannot convert to int: %#v", v)
		return 0
	}
}

func runCmdEnv(ctx context.Context, dir string, extraEnv []string, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), extraEnv...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}
