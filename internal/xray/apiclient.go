// Package xray provides Xray gRPC API client for adding users to inbounds.
// When xray_api_addr is configured, users are added via HandlerService.AlterInbound
// instead of writing to config files.
package xray

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/xtls/xray-core/app/proxyman/command"
	"github.com/xtls/xray-core/common/protocol"
	cserial "github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/proxy/shadowsocks"
	"github.com/xtls/xray-core/proxy/shadowsocks_2022"
	"github.com/xtls/xray-core/proxy/trojan"
	"github.com/xtls/xray-core/proxy/vless"
	"github.com/xtls/xray-core/proxy/vmess"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const apiDialTimeout = 10 * time.Second

func dialXrayAPI(apiAddr string) (*grpc.ClientConn, error) {
	conn, err := grpc.NewClient(apiAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("dial xray api %s: %w", apiAddr, err)
	}
	return conn, nil
}

// AddExistingClientToInboundViaAPI adds a client with existing credentials (from DB) to Xray via gRPC API.
// Used when restoring users after Xray restart. Protocol is derived from storedConfigJSON.
func AddExistingClientToInboundViaAPI(apiAddr, inboundTag, username, storedConfigJSON string) error {
	username = strings.TrimSpace(username)
	if username == "" {
		return fmt.Errorf("username required")
	}
	if apiAddr == "" {
		return fmt.Errorf("xray_api_addr required")
	}

	user, err := storedConfigToProtocolUser(storedConfigJSON, username)
	if err != nil {
		return fmt.Errorf("build protocol user: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), apiDialTimeout)
	defer cancel()

	conn, err := dialXrayAPI(apiAddr)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()

	client := command.NewHandlerServiceClient(conn)
	_, err = client.AlterInbound(ctx, &command.AlterInboundRequest{
		Tag: inboundTag,
		Operation: cserial.ToTypedMessage(&command.AddUserOperation{
			User: user,
		}),
	})
	if err != nil {
		return fmt.Errorf("alter inbound: %w", err)
	}
	return nil
}

// storedConfigToProtocolUser converts StoredClientConfig JSON to protocol.User.
func storedConfigToProtocolUser(storedJSON, email string) (*protocol.User, error) {
	var stored StoredClientConfig
	if err := json.Unmarshal([]byte(storedJSON), &stored); err != nil {
		return nil, fmt.Errorf("parse stored config: %w", err)
	}

	proto := strings.ToLower(stored.Protocol)
	switch proto {
	case "vless":
		return &protocol.User{
			Email: email,
			Account: cserial.ToTypedMessage(&vless.Account{
				Id:   stored.ID,
				Flow: firstNonEmpty(stored.Flow, ""),
			}),
		}, nil
	case "vmess":
		return &protocol.User{
			Email: email,
			Account: cserial.ToTypedMessage(&vmess.Account{
				Id: stored.ID,
			}),
		}, nil
	case "trojan":
		return &protocol.User{
			Email: email,
			Account: cserial.ToTypedMessage(&trojan.Account{
				Password: stored.Password,
			}),
		}, nil
	case "shadowsocks":
		if strings.HasPrefix(stored.Method, "2022") {
			return &protocol.User{
				Email: email,
				Account: cserial.ToTypedMessage(&shadowsocks_2022.Account{
					Key: stored.Password,
				}),
			}, nil
		}
		ct := shadowsocks.CipherType_CHACHA20_POLY1305
		switch stored.Method {
		case "aes-128-gcm":
			ct = shadowsocks.CipherType_AES_128_GCM
		case "aes-256-gcm":
			ct = shadowsocks.CipherType_AES_256_GCM
		case "chacha20-poly1305":
			ct = shadowsocks.CipherType_CHACHA20_POLY1305
		case "xchacha20-poly1305":
			ct = shadowsocks.CipherType_XCHACHA20_POLY1305
		case "none":
			ct = shadowsocks.CipherType_NONE
		}
		return &protocol.User{
			Email: email,
			Account: cserial.ToTypedMessage(&shadowsocks.Account{
				Password:   stored.Password,
				CipherType: ct,
			}),
		}, nil
	default:
		return nil, fmt.Errorf("unsupported protocol %s", stored.Protocol)
	}
}

// RemoveUserFromInboundViaAPI removes a user (by email) from the Xray inbound via gRPC API.
func RemoveUserFromInboundViaAPI(apiAddr, inboundTag, email string) error {
	email = strings.TrimSpace(email)
	if email == "" {
		return fmt.Errorf("email required")
	}
	if apiAddr == "" {
		return fmt.Errorf("xray_api_addr required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), apiDialTimeout)
	defer cancel()

	conn, err := dialXrayAPI(apiAddr)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()

	client := command.NewHandlerServiceClient(conn)
	_, err = client.AlterInbound(ctx, &command.AlterInboundRequest{
		Tag: inboundTag,
		Operation: cserial.ToTypedMessage(&command.RemoveUserOperation{
			Email: email,
		}),
	})
	if err != nil {
		return fmt.Errorf("alter inbound: %w", err)
	}
	return nil
}

// AddClientToInboundViaAPI adds a new client to the Xray inbound via gRPC API.
// configDir is used to find the inbound (protocol, settings) for building credentials.
// protocolFallback: when configDir has no inbound, use this protocol (vless, vmess, trojan, shadowsocks).
// clientEncStr is the VLESS Encryption client string (from vless_client_encryption config); empty = "none".
// Returns the client credentials JSON for UpsertUserClient, or error.
func AddClientToInboundViaAPI(apiAddr, configDir, inboundTag, username, protocolFallback, clientEncStr string) (clientConfigJSON string, err error) {
	username = strings.TrimSpace(username)
	if username == "" {
		return "", fmt.Errorf("username required")
	}
	if apiAddr == "" {
		return "", fmt.Errorf("xray_api_addr required")
	}

	var protocolName string
	var settingsRaw json.RawMessage

	_, protocolName, settingsRaw, err = findInboundSettings(configDir, inboundTag)
	if err != nil && protocolFallback != "" {
		protocolName = strings.ToLower(strings.TrimSpace(protocolFallback))
		settingsRaw = nil
		err = nil
	}
	if err != nil {
		return "", err
	}

	// Build new client (same logic as configwriter)
	var newClient map[string]interface{}
	switch strings.ToLower(protocolName) {
	case "vless":
		newClient = buildVLESSClient(username, settingsRaw)
	case "vmess":
		newClient = buildVMessClient(username, settingsRaw)
	case "trojan":
		newClient = buildTrojanClient(username)
	case "shadowsocks":
		newClient = buildShadowsocksClient(username, settingsRaw)
	case "socks":
		return "", fmt.Errorf("socks inbound does not support API user creation")
	default:
		return "", fmt.Errorf("unsupported protocol %s for inbound %s", protocolName, inboundTag)
	}

	if newClient == nil {
		return "", fmt.Errorf("failed to build client for protocol %s", protocolName)
	}

	// Build protocol.User for gRPC
	user, err := clientMapToProtocolUser(protocolName, newClient, username)
	if err != nil {
		return "", fmt.Errorf("build protocol user: %w", err)
	}

	// Dial and call API
	ctx, cancel := context.WithTimeout(context.Background(), apiDialTimeout)
	defer cancel()

	conn, err := dialXrayAPI(apiAddr)
	if err != nil {
		return "", err
	}
	defer func() { _ = conn.Close() }()

	client := command.NewHandlerServiceClient(conn)
	_, err = client.AlterInbound(ctx, &command.AlterInboundRequest{
		Tag: inboundTag,
		Operation: cserial.ToTypedMessage(&command.AddUserOperation{
			User: user,
		}),
	})
	if err != nil {
		return "", fmt.Errorf("alter inbound: %w", err)
	}

	// Return stored config for DB
	clientConfigJSON, err = clientToStoredConfig(protocolName, newClient, clientEncStr)
	if err != nil {
		return "", fmt.Errorf("client config: %w", err)
	}
	return clientConfigJSON, nil
}

// clientMapToProtocolUser converts a client map (from build*Client) to protocol.User.
func clientMapToProtocolUser(protocolName string, client map[string]interface{}, email string) (*protocol.User, error) {
	proto := strings.ToLower(protocolName)
	switch proto {
	case "vless":
		id, _ := client["id"].(string)
		flow, _ := client["flow"].(string)
		return &protocol.User{
			Email: email,
			Account: cserial.ToTypedMessage(&vless.Account{
				Id:   id,
				Flow: flow,
			}),
		}, nil
	case "vmess":
		id, _ := client["id"].(string)
		return &protocol.User{
			Email: email,
			Account: cserial.ToTypedMessage(&vmess.Account{
				Id: id,
			}),
		}, nil
	case "trojan":
		pwd, _ := client["password"].(string)
		return &protocol.User{
			Email: email,
			Account: cserial.ToTypedMessage(&trojan.Account{
				Password: pwd,
			}),
		}, nil
	case "shadowsocks":
		pwd, _ := client["password"].(string)
		method, _ := client["method"].(string)
		if strings.HasPrefix(method, "2022") {
			return &protocol.User{
				Email: email,
				Account: cserial.ToTypedMessage(&shadowsocks_2022.Account{
					Key: pwd,
				}),
			}, nil
		}
		ct := shadowsocks.CipherType_CHACHA20_POLY1305
		switch method {
		case "aes-128-gcm":
			ct = shadowsocks.CipherType_AES_128_GCM
		case "aes-256-gcm":
			ct = shadowsocks.CipherType_AES_256_GCM
		case "chacha20-poly1305":
			ct = shadowsocks.CipherType_CHACHA20_POLY1305
		case "xchacha20-poly1305":
			ct = shadowsocks.CipherType_XCHACHA20_POLY1305
		case "none":
			ct = shadowsocks.CipherType_NONE
		}
		return &protocol.User{
			Email: email,
			Account: cserial.ToTypedMessage(&shadowsocks.Account{
				Password:   pwd,
				CipherType: ct,
			}),
		}, nil
	default:
		return nil, fmt.Errorf("unsupported protocol %s", protocolName)
	}
}
