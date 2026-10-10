const root = document.querySelector('#app');
const node = (tag, text, attrs={}) => {const n=document.createElement(tag);if(text)n.textContent=text;for(const [k,v]of Object.entries(attrs))n.setAttribute(k,v);return n;};
let current;
const newConnectionId=()=>Array.from(crypto.getRandomValues(new Uint8Array(16)),value=>value.toString(16).padStart(2,'0')).join('');
let presenceConnection='',presenceActive=false;
async function heartbeat(){if(!current||document.hidden)return;if(!presenceActive){presenceConnection=newConnectionId();presenceActive=true;}try{await request('/api/auth/heartbeat',{connectionId:presenceConnection,online:true});}catch{}}
function leaveWebsite(){if(!presenceActive)return;const body=JSON.stringify({connectionId:presenceConnection,online:false});presenceActive=false;if(!navigator.sendBeacon('/api/auth/heartbeat',new Blob([body],{type:'application/json'})))fetch('/api/auth/heartbeat',{method:'POST',credentials:'same-origin',headers:{'content-type':'application/json'},body,keepalive:true}).catch(()=>{});}
setInterval(heartbeat,30000);
document.addEventListener('visibilitychange',()=>document.hidden?leaveWebsite():heartbeat());
window.addEventListener('pagehide',leaveWebsite);window.addEventListener('pageshow',heartbeat);
async function request(path, body) {const r=await fetch(path,{credentials:'same-origin',...(body===undefined?{}:{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify(body)})});const j=await r.json();if(!r.ok)throw new Error(j.error||'操作失败');return j;}
function panel(title, description) {root.replaceChildren(node('div','MCRAFT ACCOUNT',{class:'brand'}),node('h1',title),node('p',description));}
function field(form, label, name, type='text', value='') {const l=node('label',label),i=node('input','',{name,type,required:'',autocomplete:type==='password'?'current-password':'off'});i.value=value;l.append(i);form.append(l);return i;}
function button(text,fn) {const b=node('button',text,{type:'button'});b.onclick=fn;return b;}
function identity() {if(!current)return;root.append(node('small','固定玩家 ID'),node('code',current.id));}
function completed(user) {current=user;heartbeat();if(!user.name)return profile();if(location.pathname==='/authorize'){const q=new URLSearchParams(location.search);q.delete('profile');location.replace('/authorize?'+q);return;}dashboard();}
function credentials(mode='login') {
  const register=mode==='register';panel(register?'注册核心账号':'登录核心账号','账号注册后获得固定玩家 ID，可用同一身份加入所有接入核心服务的游戏服务器。');
  root.append(button('登录',()=>credentials('login')),button('注册',()=>credentials('register')));
  const form=node('form'),username=field(form,'账号','username'),password=field(form,'密码','password','password');username.autocomplete='username';username.pattern='[A-Za-z0-9_]{3,32}';username.minLength=3;username.maxLength=32;password.maxLength=72;
  if(register){password.minLength=8;password.autocomplete='new-password';}
  const confirm=register?field(form,'确认密码','confirm','password'):null;if(confirm)confirm.autocomplete='new-password';
  const error=node('p','',{class:'error',role:'alert'}),submit=node('button',register?'注册核心账号':'登录并继续',{type:'submit'});form.append(error,submit);root.append(form);
  form.onsubmit=async e=>{e.preventDefault();if(submit.disabled)return;error.textContent='';if(confirm&&confirm.value!==password.value){error.textContent='两次密码不一致';return;}submit.disabled=true;try{const {user}=await request('/api/auth/'+mode,{username:username.value.trim(),password:password.value});password.value='';if(confirm)confirm.value='';completed(user);}catch(err){error.textContent=err.message;password.value='';submit.disabled=false;}};
}
function profile() {panel('设置玩家名字','名字可以修改，固定 ID 不会改变，游戏服务器的背包和权限仍绑定原 ID。');identity();const form=node('form'),name=field(form,'玩家名字','name','text',current.name||current.username);name.minLength=2;name.maxLength=20;const error=node('p','',{class:'error',role:'alert'}),save=node('button','保存名字并继续',{type:'submit'});form.append(error,save);root.append(form);form.onsubmit=async e=>{e.preventDefault();save.disabled=true;try{const {user}=await request('/api/auth/profile',{name:name.value.trim()});completed(user);}catch(err){error.textContent=err.message;save.disabled=false;}};}
async function dashboard() {
  panel('核心账号中心',`玩家：${current.name} · 账号：${current.username}`);identity();root.append(node('p','进入统一游戏网站后，可以创建浏览器本地游戏或加入私人服务器。'),button('修改玩家名字',profile),button('退出核心账号',async()=>{try{await request('/api/auth/logout',{});leaveWebsite();current=null;credentials();}catch(err){root.append(node('p',err.message,{class:'error'}));}}),node('hr'),node('h2','服主接入自己的游戏服务器'),node('p','服主自行部署 Go 游戏服务，登记统一游戏网站地址与本服 Go 后端回调地址。将返回的服务器 ID、核心服务地址和公钥配置到该游戏服。'));
  const entry=node('div','',{class:'row'});root.insertBefore(entry,root.querySelector('hr'));
  try{const {gameHomeUrl}=await request('/api/launcher');if(gameHomeUrl){const link=node('a','进入游戏首页',{href:gameHomeUrl});entry.append(link);}}catch{}
  try{const {user}=await request('/api/admin/me');if(user)entry.append(node('a','核心账号管理后台',{href:'/admin'}));}catch{}
  const form=node('form'),name=field(form,'服务器名称','name'),frontend=field(form,'统一游戏网站地址','frontendUrl'),callback=field(form,'本服 Go 后端回调地址','callbackUrl');frontend.placeholder='https://play.example.com';callback.placeholder='https://private.example.com/api/auth/callback';const error=node('p','',{class:'error',role:'alert'}),save=node('button','登记游戏服务器',{type:'submit'});form.append(error,save);root.append(form);
  form.onsubmit=async e=>{e.preventDefault();save.disabled=true;try{const {server,issuer,publicKey}=await request('/api/servers',{name:name.value.trim(),frontendUrl:frontend.value.trim().replace(/\/$/,''),callbackUrl:callback.value.trim()});root.append(node('pre',`AUTH_ISSUER_URL=${issuer}\nAUTH_SERVER_ID=${server.id}\nAUTH_PUBLIC_KEY=${publicKey}\nGAME_PUBLIC_URL=${server.frontendUrl}\nAUTH_CALLBACK_URL=${server.callbackUrl}`));save.disabled=false;}catch(err){error.textContent=err.message;save.disabled=false;}};
  try{const {servers}=await request('/api/servers');for(const server of servers)root.append(node('p',server.name),node('code',server.id+'\n'+server.frontendUrl));}catch(err){root.append(node('p',err.message,{class:'error'}));}
}
try{const {user}=await request('/api/auth/me');current=user;if(!user)credentials();else{heartbeat();if(!user.name||new URLSearchParams(location.search).has('profile'))profile();else completed(user);}}catch(err){panel('暂时无法连接账号服务',err.message);root.append(button('重新连接',()=>location.reload()));}
