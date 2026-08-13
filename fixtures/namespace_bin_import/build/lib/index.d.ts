declare namespace geo {
  export type Point = {x: number, y: number};
  export const Point: {new (x: number, y: number): Point};
  export function makeOrigin(): Point;
}
