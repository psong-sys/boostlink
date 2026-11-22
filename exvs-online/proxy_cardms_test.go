package main

import (
	"testing"

	"exvsonline/pb/exvs"

	"google.golang.org/protobuf/proto"
)

func TestPatchCardMobileSuit(t *testing.T) {
	p := &Proxy{cfg: ProxyConfig{}}
	pl := &exvs.Response_PreLoadCard{User: &exvs.Response_PreLoadCard_MobileUserGroup{
		CustomizeGroup: &exvs.Response_PreLoadCard_MobileUserGroup_CustomizeGroup{MstMobileSuitId: proto.Uint32(0)}}}
	if p.patchCardMobileSuit(pl) {
		t.Fatal("patched with no suit configured")
	}
	p.cfg.CardMobileSuit = 1
	if !p.patchCardMobileSuit(pl) || pl.User.CustomizeGroup.GetMstMobileSuitId() != 1 {
		t.Fatalf("not patched: %v", pl)
	}
	if p.patchCardMobileSuit(pl) {
		t.Error("re-patched an already set suit")
	}
	if p.patchCardMobileSuit(nil) || p.patchCardMobileSuit(&exvs.Response_PreLoadCard{}) {
		t.Error("patched a reply without a user")
	}
}
