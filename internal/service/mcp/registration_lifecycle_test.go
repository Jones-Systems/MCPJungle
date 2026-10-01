package mcp

import (
 "bufio"
 "context"
 "encoding/json"
 "fmt"
 "os"
 "path/filepath"
 "strconv"
 "strings"
 "syscall"
 "testing"
 "time"

 "github.com/mcpjungle/mcpjungle/internal/model"
 "github.com/mcpjungle/mcpjungle/pkg/types"
)

// The child writes only beneath the caller-owned fixture root and exits on stdin closure.
func TestRegistrationStdioFixture(t *testing.T) {
 if os.Getenv("MCPJUNGLE_LIFECYCLE_FIXTURE") != "1" { return }
 root := os.Getenv("MCPJUNGLE_FIXTURE_ROOT")
 _ = os.WriteFile(filepath.Join(root, "pid"), []byte(strconv.Itoa(os.Getpid())), 0600)
 _ = os.WriteFile(filepath.Join(root,"generation"), []byte(fixtureGeneration(os.Getpid())),0600)
 scanner := bufio.NewScanner(os.Stdin)
 for scanner.Scan() {
  var req struct { ID json.RawMessage `json:"id"`; Method string `json:"method"` }
  if json.Unmarshal(scanner.Bytes(), &req) != nil || len(req.ID)==0 { continue }
  var result any
  switch req.Method {
  case "initialize":
   if os.Getenv("MCPJUNGLE_FIXTURE_NO_INIT")=="1" { continue }
   result = map[string]any{"protocolVersion":"2025-03-26","serverInfo":map[string]string{"name":"fixture","version":"1"},"capabilities":map[string]any{"tools":map[string]any{}}}
  case "tools/list":
   _ = os.WriteFile(filepath.Join(root,"listing"), []byte("1"), 0600)
   if os.Getenv("MCPJUNGLE_FIXTURE_BLOCK_LIST")=="1" {
    for { if _,err:=os.Stat(filepath.Join(root,"release")); err==nil { break }; time.Sleep(5*time.Millisecond) }
   }
   result = map[string]any{"tools":[]any{map[string]any{"name":"echo","inputSchema":map[string]any{"type":"object"}}}}
  case "tools/call": result=map[string]any{"content":[]any{map[string]any{"type":"text","text":"ok"}}}
  default: result=map[string]any{}
  }
  data,_:=json.Marshal(map[string]any{"jsonrpc":"2.0","id":req.ID,"result":result})
  fmt.Println(string(data))
 }
 os.Exit(0)
}

func registrationFixture(t *testing.T, name string, extra map[string]string) (*model.McpServer,string) {
 t.Helper()
 root:=t.TempDir()
 exe,err:=os.Executable(); if err!=nil { t.Fatal(err) }
 env:=map[string]string{"MCPJUNGLE_LIFECYCLE_FIXTURE":"1","MCPJUNGLE_FIXTURE_ROOT":root}
 for k,v:=range extra { env[k]=v }
 s,err:=model.NewStdioServer(name,"",exe,[]string{"-test.run=^TestRegistrationStdioFixture$"},env,types.SessionModeStateless)
 if err!=nil { t.Fatal(err) }
 t.Cleanup(func(){
  data,err:=os.ReadFile(filepath.Join(root,"pid")); if err!=nil { return }
  pid,_:=strconv.Atoi(string(data)); generation,err:=os.ReadFile(filepath.Join(root,"generation"))
  if err!=nil || len(generation)==0 || fixtureGeneration(pid)!=string(generation) { return }
  _=syscall.Kill(pid,syscall.SIGKILL)
  // The SDK owns Wait; after forcing our verified child, wait for disappearance or zombie exit.
  deadline:=time.Now().Add(time.Second)
  for time.Now().Before(deadline) {
   if fixtureGeneration(pid)!=string(generation) { return }
   stat,_:=os.ReadFile(fmt.Sprintf("/proc/%d/stat",pid)); tail:=strings.Fields(string(stat)[strings.LastIndex(string(stat),")")+1:])
   if len(tail)>0 && tail[0]=="Z" { return }
   time.Sleep(5*time.Millisecond)
  }
  t.Errorf("verified fixture process did not exit")
 })
 return s,root
}

func TestRegistrationFailedInitializeClosesClient(t *testing.T) {
 s,root:=registrationFixture(t,"failed-init",map[string]string{"MCPJUNGLE_FIXTURE_NO_INIT":"1"})
 ctx,cancel:=context.WithTimeout(context.Background(),100*time.Millisecond); defer cancel()
 if _,err:=runStdioServer(ctx,s,1); err==nil { t.Fatal("expected initialization timeout") }
 data,err:=os.ReadFile(filepath.Join(root,"pid")); if err!=nil { t.Fatal(err) }
 pid,err:=strconv.Atoi(string(data)); if err!=nil { t.Fatal(err) }
 deadline:=time.Now().Add(time.Second)
 for time.Now().Before(deadline) {
  if syscall.Kill(pid,0)!=nil { return }
  time.Sleep(5*time.Millisecond)
 }
 t.Fatal("failed initialization left temporary MCP client process alive")
}

func fixtureGeneration(pid int) string {
 if pid<=0 { return "" }
 stat,err:=os.ReadFile(fmt.Sprintf("/proc/%d/stat",pid)); if err!=nil { return "" }
 end:=strings.LastIndex(string(stat),")"); if end<0 { return "" }
 fields:=strings.Fields(string(stat)[end+1:]); if len(fields)<20 { return "" }
 boot,err:=os.ReadFile("/proc/sys/kernel/random/boot_id"); if err!=nil { return "" }
 return strings.TrimSpace(string(boot))+":"+fields[19]
}
