export declare const callPriv: number;
export declare const callPub: number;
export declare const readPrivVal: 20;
export declare const readPubVal: 10;
declare const rootPriv: 2;
export declare const rootPub: 1;
declare namespace geo {
  function priv(): number;
  export function pub(): number;
  const privVal: 20;
  export const pubVal: 10;
}
declare namespace geo {
  namespace inner {
    const deepPriv: 200;
    export const deepPub: 100;
  }
}
