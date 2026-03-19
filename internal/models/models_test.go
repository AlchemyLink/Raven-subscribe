package models

import (
	"encoding/json"
	"testing"
)

func TestCreateUserRequest_JSON(t *testing.T) {
	req := CreateUserRequest{
		Username: "alice",
		Inbounds: []InboundSpec{{Tag: "vless-in", Protocol: "vless"}},
	}
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var decoded CreateUserRequest
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if decoded.Username != req.Username {
		t.Errorf("Username: got %q, want %q", decoded.Username, req.Username)
	}
	if len(decoded.Inbounds) != 1 || decoded.Inbounds[0].Tag != "vless-in" {
		t.Errorf("Inbounds: got %+v", decoded.Inbounds)
	}
}

func TestCreateUserRequest_EmptyInbounds(t *testing.T) {
	data := []byte(`{"username":"bob"}`)
	var req CreateUserRequest
	if err := json.Unmarshal(data, &req); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if req.Username != "bob" {
		t.Errorf("Username: got %q", req.Username)
	}
	if req.Inbounds != nil {
		t.Errorf("Inbounds: expected nil, got %+v", req.Inbounds)
	}
}

func TestUserResponse_JSON(t *testing.T) {
	resp := UserResponse{
		User:   User{ID: 1, Username: "alice", Token: "t1", Enabled: true},
		SubURL: "https://vpn.example.com/sub/t1",
	}
	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var decoded UserResponse
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if decoded.User.ID != resp.User.ID {
		t.Errorf("User.ID: got %d", decoded.User.ID)
	}
	if decoded.SubURL != resp.SubURL {
		t.Errorf("SubURL: got %q", decoded.SubURL)
	}
}

func TestUserRouteRule_JSON(t *testing.T) {
	rule := UserRouteRule{
		ID:           "r1",
		OutboundTag:  "direct",
		Domain:       []string{"example.com"},
		Type:         "field",
	}
	data, err := json.Marshal(rule)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var decoded UserRouteRule
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if decoded.OutboundTag != rule.OutboundTag {
		t.Errorf("OutboundTag: got %q", decoded.OutboundTag)
	}
	if len(decoded.Domain) != 1 || decoded.Domain[0] != "example.com" {
		t.Errorf("Domain: got %+v", decoded.Domain)
	}
}
