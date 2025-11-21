declare type Point = {x: number, y: number};
declare const p1: {x: 5, y: 10};
declare const q1: Point;
declare type P = typeof p1;
declare type Q = typeof q1;
declare const p2: P;
declare const q2: Q;
declare type X = typeof p1.x;
declare type Y = typeof q1.y;
declare const x: X;
declare const y: Y;
declare type CX = typeof shapes.unitCircle.center.x;
declare const cx: CX;
declare namespace shapes {
  const unitCircle: {center: {x: 0, y: 0}, radius: 1};
  type Circle = {center: Point, radius: number};
}
