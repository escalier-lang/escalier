const app = {};
app.utils = {};
export const app__config = {multiplier: 2};
app.config = app__config;
export const app__utils__factor = app.config.multiplier;
app.utils.factor = app__utils__factor;
export function app__utils__calculate(temp1) {
  const x = temp1;
  return x * app.utils.factor;
}
app.utils.calculate = app__utils__calculate;
//# sourceMappingURL=./index.js.map
