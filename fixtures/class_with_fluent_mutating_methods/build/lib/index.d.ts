declare type Point = {x: number, y: number, scale(factor: number): Point, translate(dx: number, dy: number): Point};
declare const Point: {new (x: number, y: number): Point};
declare const p: Point;
declare const q: Point;
declare function sqrt(x: number): number;
