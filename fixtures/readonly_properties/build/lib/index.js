export class Counter {
  constructor(temp1) {
    const value = temp1;
    this.value = value;
  }
  increment() {
    this.value = this.value + 1;
  }
}
export class Point {
  constructor(temp2, temp3) {
    const x = temp2;
    const y = temp3;
    this.x = x;
    this.y = y;
  }
}
export const config = {apiUrl: "https://api.example.com", timeout: 5000};
export const apiUrl = config.apiUrl;
export const counter = new Counter(0);
export const immutablePoint = new Point(5, 15);
export const immutableX = immutablePoint.x;
export const point = new Point(10, 20);
export const pointX = point.x;
export function tryUpdateImmutableProperties() {
  config.apiUrl = "https://foo.example.com";
  point.x = 0;
}
export function updateMutableProperties() {
  point.y = 25;
}
//# sourceMappingURL=./index.js.map
