package game

import (
 "net/http/httptest"
 "os"
 "path/filepath"
 "testing"
)

func TestInfiniteLegacyCreativeHotbar(t *testing.T) {
 dir:=t.TempDir();os.MkdirAll(filepath.Join(dir,"rooms"),0700)
 raw:=`{"version":6,"name":"旧创造世界","mode":"creative","players":{"LegacyHotbar123":{"x":512.5,"y":90,"z":512.5,"hotbar":[82,3,1,4,5,6,10,11,7],"slot":0,"health":20,"food":20}}}`
 if err:=os.WriteFile(filepath.Join(dir,"rooms",DefaultRoom+".json"),[]byte(raw),0600);err!=nil {t.Fatal(err)}
 s,err:=New(Config{DataDir:dir,EngineFile:"../../../public/engine.wasm",InfiniteWorlds:true,AllowedOrigins:[]string{"http://127.0.0.1:8080"}});if err!=nil {t.Fatal(err)};h:=httptest.NewServer(s.Handler());defer h.Close();defer s.Close()
 c,w:=connect(t,h.URL,DefaultRoom,"LegacyHotbar123","");defer c.Close();st:=w["st"].(map[string]any);inv:=st["inv"].(map[string]any);slots:=inv["s"].([]any)
 if slots[0].([]any)[0]!=float64(82)||slots[1].([]any)[0]!=float64(3)||inv["sel"]!=float64(0){t.Fatal("legacy creative hotbar replaced by starter items")}
}
