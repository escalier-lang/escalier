declare type Point = {x: number, y: number};
declare const p1: {x: 5, y: 10};
declare const q1: Point;
declare type P = typeof p1;
declare type Q = typeof q1;
declare const p2: P;
declare const q2: Q;
