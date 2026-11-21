package main

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"exvsonline/pb/mms"

	"google.golang.org/protobuf/proto"
)

func TestStatusPage(t *testing.T) {
	mc := defaultConfig().Match
	mc.RoomSize = 4
	px, up := newTestProxy(t, mc)
	defer px.Close()
	defer up.Close()
	// One solo player queues.
	id := &mms.Response{}
	post(t, px.URL+"/match", &mms.Request{Type: mms.MethodType_ISSUE_NODE_ID.Enum(), RequestId: proto.String("i"),
		IssueNodeId: &mms.Request_IssueNodeId{IssueCount: proto.Uint32(1)}}, id)
	node := id.GetIssueNodeId().GetNodeId()[0]
	post(t, px.URL+"/match", &mms.Request{Type: mms.MethodType_ENTRY_MATCHING.Enum(), RequestId: proto.String("e"),
		EntryMatching: &mms.Request_EntryMatching{Myclient: client(node, "198.51.100.1")}}, &mms.Response{})

	r, err := http.Get(px.URL + "/status.json")
	if err != nil {
		t.Fatal(err)
	}
	var st Status
	_ = json.NewDecoder(r.Body).Decode(&st)
	r.Body.Close()
	if st.Searching != 1 || len(st.WaitingGroups) != 1 {
		t.Fatalf("status.json: %+v", st)
	}
	r, _ = http.Get(px.URL + "/status")
	b, _ := io.ReadAll(r.Body)
	r.Body.Close()
	if !strings.Contains(string(b), "searching now") || strings.Contains(string(b), "198.51.100.1") {
		t.Fatalf("status page wrong or leaks IPs")
	}
}
