const { test } = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");

test("recording binds queue, credentials, origins and engagement before any upload", async () => {
  let config = {serverUrl:"https://first.invalid", token:"first", taskId:"11", origins:""};
  const handlers = {}, sent = [];
  const event = name => ({addListener(fn) { handlers[name] = fn; }});
  const sandbox = {
    URL, chrome:{
      storage:{local:{get:async()=>({...config})},session:{set:async()=>{}}},
      tabs:{query:async()=>[{id:3,url:"https://client.invalid/home"}]},
      debugger:{attach:async()=>{},detach:async()=>{},sendCommand:async()=>({body:"sample"}),onEvent:event("event"),onDetach:event("detach")},
      action:{setBadgeText(){},setBadgeBackgroundColor(){}},runtime:{onMessage:event("message")}
    },
    fetch:async(url,opts)=>{ sent.push({url,opts}); return {ok:true,json:async()=>({ingested:1})}; }
  };
  vm.runInNewContext(fs.readFileSync(path.join(__dirname,"background.js"),"utf8"),sandbox);
  const msg = type => new Promise(resolve => handlers.message({type},null,resolve));
  assert.equal((await msg("start")).ok,true);
  const capture = (id,url) => {
    handlers.event({tabId:3},"Network.requestWillBeSent",{requestId:id,request:{url,method:"GET",headers:{}}});
    handlers.event({tabId:3},"Network.responseReceived",{requestId:id,response:{url,status:200,headers:{}}});
    handlers.event({tabId:3},"Network.loadingFinished",{requestId:id});
  };
  capture("1","https://client.invalid/approved");
  capture("2","https://another.invalid/private");
  await new Promise(resolve=>setImmediate(resolve));
  config = {serverUrl:"https://second.invalid", token:"second", taskId:"22"};
  await msg("flushNow");
  assert.equal(sent.length,1);
  assert.equal(sent[0].url,"https://first.invalid/api/traffic/ingest");
  assert.equal(sent[0].opts.headers.Authorization,"Bearer first");
  assert.equal(sent[0].opts.redirect,"error");
  const batch = JSON.parse(sent[0].opts.body);
  assert.equal(batch.task_id,11);
  assert.deepEqual(batch.exchanges.map(e=>e.url),["https://client.invalid/approved"]);
});
