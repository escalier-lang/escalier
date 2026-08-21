declare namespace geo {
  type Point = {x: number, y: number};
  const Point: {new (x: number, y: number): Point};
  function makeOrigin(): Point;
}
