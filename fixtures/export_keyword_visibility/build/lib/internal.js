export const geo = {};
geo.inner = {};
function geo__priv() {
  return 2;
}
geo.priv = geo__priv;
export const callPriv = geo__priv();
function geo__pub() {
  return 1;
}
geo.pub = geo__pub;
export const callPub = geo.pub();
const geo__inner__deepPriv = 200;
geo.inner.deepPriv = geo__inner__deepPriv;
const geo__inner__deepPub = 100;
geo.inner.deepPub = geo__inner__deepPub;
const geo__privVal = 20;
geo.privVal = geo__privVal;
const geo__pubVal = 10;
geo.pubVal = geo__pubVal;
export const readPrivVal = geo__privVal;
export const readPubVal = geo.pubVal;
export const rootPriv = 2;
export const rootPub = 1;
//# sourceMappingURL=./internal.js.map
