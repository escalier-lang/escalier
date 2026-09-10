declare type Box = {x: number, y: number, grow(d: number): number, grow(d: number, e: number): number, area(): number};
declare const Box: {new (x: number, y: number): Box, of(x: number): number, of(x: number, y: number): number};
