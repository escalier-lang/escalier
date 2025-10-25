declare type A = "a";
declare type B = "b";
declare type AB = A | B;
declare type C = "c";
declare type T = `${AB}-${C}`;
declare const x: T;
declare const y: T;
declare const z: T;
