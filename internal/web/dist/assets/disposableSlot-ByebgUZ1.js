function r(o){let t;return{setup:()=>(t==null||t.dispose(),t=o(),t),teardown:()=>{const e=t;t=void 0,e==null||e.dispose()},current:()=>t}}export{r as c};
