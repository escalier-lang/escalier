export const geo = {};
function geo__priv() {
  return 2;
}
export const callPriv = geo__priv();
function geo__pub() {
  return 1;
}
geo.pub = geo__pub;
export const callPub = geo.pub();
const geo__privVal = 20;
const geo__pubVal = 10;
geo.pubVal = geo__pubVal;
export const readPrivVal = geo__privVal;
export const readPubVal = geo.pubVal;
export const rootPriv = 2;
export const rootPub = 1;
//# sourceMappingURL=./index.js.map
