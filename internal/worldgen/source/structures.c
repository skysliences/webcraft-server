// Shared deterministic structure generator. No platform RNG, heap, save writes,
// or access to neighboring generated chunks. Hosts provide *base* terrain only.
// Block IDs are the engine's stable native registry, all below 256.
typedef unsigned int u32;
__attribute__((import_module("terrain"), import_name("height"))) int wg_height(int x,int z);
__attribute__((import_module("terrain"), import_name("biome"))) int wg_biome(int x,int z);
__attribute__((import_module("terrain"), import_name("write"))) void wg_write(int x,int y,int z,int block);
typedef struct { int kind,x,y,z,style; u32 seed; } Event;
#define MAX_EVENTS 2048
static Event events[MAX_EVENTS];
static int count,chunkx,chunkz;
static u32 random_state;
static int abs_i(int n){return n<0?-n:n;}
static int min_i(int a,int b){return a<b?a:b;}
static int max_i(int a,int b){return a>b?a:b;}
static int floor_div(int n,int d){int q=n/d;return q-(n%d<0);}
static u32 hash(u32 s,int x,int z,u32 salt){u32 h=s^((u32)x*0x8da6b343u)^((u32)z*0xd8163841u)^salt;h=(h^(h>>16))*0x7feb352du;h=(h^(h>>15))*0x846ca68bu;return h^(h>>16);}
static u32 next(void){random_state^=random_state<<13;random_state^=random_state>>17;random_state^=random_state<<5;return random_state;}
static int inside(int x,int z){return floor_div(x,16)==chunkx&&floor_div(z,16)==chunkz;}
static void put(int x,int y,int z,int b){if(y>0&&y<127&&inside(x,z))wg_write(x,y,z,b);}
static void event(int kind,int x,int y,int z,int style,u32 seed){if(inside(x,z)&&count<MAX_EVENTS)events[count++]=(Event){kind,x,y,z,style,seed};}
static void box(int x0,int y0,int z0,int x1,int y1,int z1,int b){
  x0=max_i(x0,chunkx*16);x1=min_i(x1,chunkx*16+15);z0=max_i(z0,chunkz*16);z1=min_i(z1,chunkz*16+15);
  for(int z=z0;z<=z1;z++)for(int x=x0;x<=x1;x++)for(int y=max_i(1,y0);y<=min_i(126,y1);y++)put(x,y,z,b);
}
int wg_meta_ptr(void){return (int)events;}
int wg_meta_count(void){return count;}
int wg_version(void){return 1;}

// Palette order: wall, beam, roof, foundation, path. Acacia and terracotta
// are unavailable in the host; savanna uses orange plaster and oak beams.
static const int palettes[5][5]={{5,6,5,4,13},{15,15,15,15,8},{48,6,20,4,13},{19,72,19,4,13},{19,72,14,4,13}};
static const int dx[4]={0,1,0,-1},dz[4]={1,0,-1,0};
static int ox,oz,dir,level,theme;
static void local(int u,int y,int v,int b){put(ox+dx[dir]*v+dz[dir]*u,y,oz+dz[dir]*v-dx[dir]*u,b);}
static int terrain(int u,int v){return wg_height(ox+dx[dir]*v+dz[dir]*u,oz+dz[dir]*v-dx[dir]*u);}
static int touches(int radius,int depth){
  int x0=ox+dx[dir]*depth/2,z0=oz+dz[dir]*depth/2;
  int rx=abs_i(dx[dir])*(depth/2+2)+abs_i(dz[dir])*(radius+1);
  int rz=abs_i(dz[dir])*(depth/2+2)+abs_i(dx[dir])*(radius+1);
  return x0+rx>=chunkx*16&&x0-rx<=chunkx*16+15&&z0+rz>=chunkz*16&&z0-rz<=chunkz*16+15;
}
static void local_event(int kind,int u,int y,int v,u32 seed){event(kind,ox+dx[dir]*v+dz[dir]*u,y,oz+dz[dir]*v-dx[dir]*u,theme,seed);}
static int site(int x,int z,int rotation,int width,int depth){
  ox=x;oz=z;dir=rotation;int lo=127,hi=0,sum=0,n=0;
  for(int v=0;v<=depth;v+=max_i(1,depth/2))for(int u=-width;u<=width;u+=width){int h=terrain(u,v);if(h<1||h>104)return 0;int b=wg_biome(ox+dx[dir]*v+dz[dir]*u,oz+dz[dir]*v-dx[dir]*u);if(b<0)return 0;lo=min_i(lo,h);hi=max_i(hi,h);sum+=h;n++;}
  if(hi-lo>6)return 0;level=sum/n;return 1;
}
static void foundation(int radius,int depth){
  for(int v=0;v<=depth;v++)for(int u=-radius;u<=radius;u++){
    int h=terrain(u,v);for(int y=min_i(h,level)-1;y<=level;y++)local(u,y,v,palettes[theme][3]);
    for(int y=level+1;y<=max_i(level+9,h+7);y++)local(u,y,v,0);
  }
}
static void farm(u32 seed){
  if(!touches(3,7))return;
  foundation(3,7);
  for(int v=0;v<=7;v++)for(int u=-3;u<=3;u++){
    int border=u==-3||u==3||v==0||v==7;
    local(u,level,v,border?palettes[theme][1]:u==0?9:146);
    if(!border&&u)local(u,level+1,v,157);
  }
  local_event(3,3,level+1,0,seed);
}
static void house(int type,u32 seed){
  int radius=type==3?3:2,depth=type==2?8:6;
  if(!touches(radius,depth))return;
  foundation(radius,depth);
  int wall=palettes[theme][0],beam=palettes[theme][1],roof=palettes[theme][2];
  for(int y=1;y<=4;y++)for(int v=0;v<=depth;v++)for(int u=-radius;u<=radius;u++){
    int edge=u==-radius||u==radius||v==0||v==depth;
    int corner=(u==-radius||u==radius)&&(v==0||v==depth);
    local(u,level+y,v,edge?(corner?beam:wall):0);
    if(edge&&!corner&&y==2&&((v==depth&&u==0)||((u==-radius||u==radius)&&v==3)))local(u,level+y,v,10);
  }
  // Entrance faces back toward its road connector; full two-block doors.
  const int doors[4]={110,112,111,113};
  local(0,level+1,0,doors[dir]);local(0,level+2,0,118);
  if(theme==1||theme==2){
    for(int v=0;v<=depth;v++)for(int u=-radius;u<=radius;u++)local(u,level+5,v,roof);
    if(theme==1)for(int v=0;v<=depth;v++)for(int u=-radius;u<=radius;u++)if(abs_i(u)==radius||v==0||v==depth)local(u,level+6,v,15);
  }else{
    for(int u=-radius-1;u<=radius+1;u++)for(int v=-1;v<=depth+1;v++){
      int top=level+5+radius-abs_i(u);local(u,top,v,roof);
      for(int y=level+5;y<top;y++)if(v==0||v==depth)local(u,y,v,wall);
    }
  }
  // Beds follow the same local coordinate transform as the entrance.
  const int beds[4]={133,135,132,134},heads[4]={137,139,136,138};
  local(-1,level+1,depth-2,beds[dir]);local(-1,level+1,depth-1,heads[dir]);
  local(1,level+1,depth-1,type==2?25:type==3?87:26);
  if(type==2)for(int v=2;v<depth-1;v++)local(-radius+1,level+1,v,25);
  local(1,level+1,2,140);local_event(1,1,level+1,2,seed);
  local(0,level+1,depth-1,82);
  local_event(3,0,level+1,2,seed);
}
static int road(int x,int z,int rotation,int length,int style){
  int last=wg_height(x,z);
  for(int v=0;v<=length;v++){
    int px=x+dx[rotation]*v,pz=z+dz[rotation]*v,h=wg_height(px,pz);
    if(h<1||h>105||abs_i(h-last)>1||wg_biome(px,pz)<0)return v;
    for(int u=-1;u<=1;u++){
      int xx=px+dz[rotation]*u,zz=pz-dx[rotation]*u,t=wg_height(xx,zz);
      for(int y=min_i(t,h)-1;y<=h;y++)put(xx,y,zz,palettes[style][4]);
      for(int y=h+1;y<=max_i(h+4,t+6);y++)put(xx,y,zz,0);
    }
    last=h;
  }return length;
}
static void village(u32 seed,int x,int z,int style){
  if(style<0||style>4)return;
  theme=style;if(!site(x,z-4,0,4,8))return;int center=level;
  // A meeting-point template, with independently selected road/house pools.
  for(int zz=z-4;zz<=z+4;zz++)for(int xx=x-4;xx<=x+4;xx++)
    box(xx,min_i(wg_height(xx,zz),center)-1,zz,xx,center,zz,palettes[style][3]);
  box(x-4,center,z-4,x+4,center,z+4,palettes[style][4]);
  box(x-4,center+1,z-4,x+4,center+8,z+4,0);
  box(x-2,center,z-2,x+2,center+1,z+2,palettes[style][3]);
  box(x-1,center,z-1,x+1,center+1,z+1,9);
  for(int a=-2;a<=2;a+=4)for(int b=-2;b<=2;b+=4)box(x+a,center+2,z+b,x+a,center+4,z+b,119);
  box(x-2,center+5,z-2,x+2,center+5,z+2,palettes[style][2]);
  for(int d=0;d<4;d++){
    u32 arm=hash(seed,x+d,z,0x76696c6cu);int length=24+(arm%3)*12;
    int end=road(x+dx[d]*4,z+dz[d]*4,d,length,style);
    for(int v=10;v<end;v+=12)for(int side=-1;side<=1;side+=2){
      u32 h=hash(arm,v,side,0x706f6f6cu);int type=h%8;type=type<4?0:type<6?1:type==6?2:3;
      int rotation=(d+(side>0?1:3))&3,px=x+dx[d]*(v+4)+dx[rotation]*4,pz=z+dz[d]*(v+4)+dz[rotation]*4;
      if(!site(px,pz,rotation,type==3?3:type==1?3:2,type==2?8:type==1?7:6))continue;
      int entry=level;
      if(type==1)farm(h);else house(type,h);
      // Short terrain-matched connector and a solid doorstep.
      for(int k=1;k<=3;k++){
        int xx=px-dx[rotation]*k,zz=pz-dz[rotation]*k,ground=wg_height(xx,zz),y=entry+(ground-entry)*k/3;
        for(int yy=min_i(ground,y)-1;yy<=y;yy++)put(xx,yy,zz,palettes[style][4]);
        for(int yy=y+1;yy<=max_i(y+3,ground+3);yy++)put(xx,yy,zz,0);
      }
    }
  }
}

typedef struct{int x,y,z,d,depth,parent;} Connector;
typedef struct{int x0,z0,x1,z1,y0,y1;} Bounds;
static Connector queue[128];static Bounds pieces[48];
static void mine(u32 seed,int x,int z){
  int surface=wg_height(x,z);if(surface<35)return;
  int y=min_i(18+(seed%24),surface-14);if(y<15)return;
  random_state=seed|1;
  box(x-4,y-1,z-4,x+4,y-1,z+4,5);box(x-4,y,z-4,x+4,y+3,z+4,0);
  for(int a=-4;a<=4;a+=8)for(int b=-4;b<=4;b+=8)box(x+a,y,z+b,x+a,y+2,z+b,6);
  box(x-4,y+3,z-4,x+4,y+3,z-4,5);box(x-4,y+3,z+4,x+4,y+3,z+4,5);
  put(x-3,y,z-3,140);event(2,x-3,y,z-3,0,seed);
  int head=0,tail=0,n=1;pieces[0]=(Bounds){x-4,z-4,x+4,z+4,y-1,y+3};
  for(int d=0;d<4;d++)queue[tail++]=(Connector){x+dx[d]*5,y,z+dz[d]*5,d,0,0};
  while(head<tail&&n<48){
    Connector c=queue[head++];if(c.depth>5)continue;
    int length=10+(next()%3)*5,down=(next()%7==0&&c.y>20)?3:0;
    int ex=c.x+dx[c.d]*(length-1),ez=c.z+dz[c.d]*(length-1);
    if(abs_i(ex-x)>68||abs_i(ez-z)>68)continue;
    Bounds b={min_i(c.x,ex)-1,min_i(c.z,ez)-1,max_i(c.x,ex)+1,max_i(c.z,ez)+1,c.y-down-1,c.y+2};
    int reject=0;
    for(int i=0;i<n;i++){Bounds a=pieces[i];if(i!=c.parent&&b.x0<=a.x1&&b.x1>=a.x0&&b.z0<=a.z1&&b.z1>=a.z0&&b.y0<=a.y1&&b.y1>=a.y0){reject=1;break;}}
    for(int k=0;k<length&&!reject;k+=4)if(wg_height(c.x+dx[c.d]*k,c.z+dz[c.d]*k)<c.y+8)reject=1;
    if(reject)continue;
    int parent=n;pieces[n++]=b;
    for(int v=0;v<length;v++){
      int px=c.x+dx[c.d]*v,pz=c.z+dz[c.d]*v,py=c.y-min_i(down,v/3);
      for(int u=-1;u<=1;u++){
        int xx=px+dz[c.d]*u,zz=pz-dx[c.d]*u;
        put(xx,py-1,zz,5);for(int yy=0;yy<3;yy++)put(xx,py+yy,zz,0);
      }
      if(v%5==2){
        for(int side=-1;side<=1;side+=2)for(int yy=0;yy<2;yy++)put(px+dz[c.d]*side,py+yy,pz-dx[c.d]*side,119);
        for(int u=-1;u<=1;u++)put(px+dz[c.d]*u,py+2,pz-dx[c.d]*u,5);
      }
      u32 detail=hash(seed,px,pz,123);
      // Stair pieces remain traversable; only flat pieces carry rails.
      if(!down&&detail%9){put(px,py,pz,211);event(4,px,py,pz,c.d&1,detail);}
      if(v%5!=2&&detail%11==0)put(px+dz[c.d],py+2,pz-dx[c.d],206);
      if(v==length-3&&detail%3==0){put(px+dz[c.d],py,pz-dx[c.d],140);event(2,px+dz[c.d],py,pz-dx[c.d],0,detail);}
    }
    int ey=c.y-down;
    // End crossings leave a three-block wide opening to each child connector.
    for(int k=-1;k<=1;k++){
      int d=(c.d+k+4)&3;if(k&&next()%3==0)continue;
      if(tail<128)queue[tail++]=(Connector){ex+dx[d]*2,ey,ez+dz[d]*2,d,c.depth+1,parent};
      for(int t=0;t<=1;t++)for(int u=-1;u<=1;u++){
        int xx=ex+dx[d]*t+dz[d]*u,zz=ez+dz[d]*t-dx[d]*u;
        put(xx,ey-1,zz,5);for(int yy=0;yy<3;yy++)put(xx,ey+yy,zz,0);
      }
    }
  }
}
void wg_generate(u32 seed,int cx,int cz){
  chunkx=cx;chunkz=cz;count=0;
  // Random-spread village placement: 32 chunks spacing, 8 separation.
  for(int rz=floor_div(cz*16-80,512);rz<=floor_div(cz*16+95,512);rz++)for(int rx=floor_div(cx*16-80,512);rx<=floor_div(cx*16+95,512);rx++){
    u32 h=hash(seed,rx,rz,10387312u);int x=(rx*32+(h%24))*16+8,z=(rz*32+(hash(h,rx,rz,1)%24))*16+8;
    if(abs_i(x-(cx*16+8))>80||abs_i(z-(cz*16+8))>80)continue;
    village(h,x,z,wg_biome(x,z));
  }
  // Mineshaft start chance 0.4% per chunk, bounded recursive piece expansion.
  for(int z=cz-5;z<=cz+5;z++)for(int x=cx-5;x<=cx+5;x++){
    u32 h=hash(seed,x,z,0x4d494e45u);if(h%1000<4)mine(h,x*16+8,z*16+8);
  }
}
