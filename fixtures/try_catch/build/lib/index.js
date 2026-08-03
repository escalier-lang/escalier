let temp1;
try {
  temp1 = "success";
} catch (__error) {
  temp1 = "error";
}
export const basicTryCatch = temp1;
let temp2;
try {
  const x = 5;
  temp2 = x + 10;
} catch (__error) {
  const y = 0;
  temp2 = y;
}
export const blockBody = temp2;
let temp3;
try {
  throw "x";
} catch (__error) {
  const e = __error;
  temp3 = e;
}
export const catchAllFirst = temp3;
export function guardedIdent(temp4) {
  const n = temp4;
  let temp5;
  try {
    throw "x";
  } catch (__error) {
    const e = __error;
    if (e == "x" && n > 0) {
      temp5 = 0;
    } else {
      throw __error;
    }
  }
  return temp5;
}
export function guardedRefutable(temp6) {
  const n = temp6;
  let temp7;
  try {
    throw "x";
  } catch (__error) {
    let temp8 = false;
    if (__error == "x") {
      if (n > 0) {
        temp7 = 0;
        temp8 = true;
      }
    }
    if (!temp8) {
      temp7 = 1;
    }
  }
  return temp7;
}
export function guardedWildcard(temp9) {
  const n = temp9;
  let temp10;
  try {
    throw "x";
  } catch (__error) {
    if (n > 0) {
      temp10 = 0;
    } else {
      throw __error;
    }
  }
  return temp10;
}
let temp11;
try {
  temp11 = 42;
} catch (__error) {
  temp11 = "error";
}
export const mixedReturn = temp11;
let temp12;
try {
  throw Error("fail");
} catch (__error) {
  const Error = __error;
  temp12 = "caught error";
}
export const multipleCases = temp12;
let temp13;
try {
  let temp14;
  try {
    temp14 = 5;
  } catch (__error) {
    temp14 = 10;
  }
  temp13 = temp14;
} catch (__error) {
  temp13 = 0;
}
export const nestedTryCatch = temp13;
let temp15;
try {
  throw {message: "fail"};
} catch (__error) {
  if (__error != null && "message" in __error) {
    const {message: msg} = __error;
    temp15 = msg;
  } else {
    temp15 = "unknown";
  }
}
export const objectPattern = temp15;
let temp16;
try {
  throw "fail";
} catch (__error) {
  const msg = __error;
  temp16 = msg;
}
export const patternBinding = temp16;
export function safeDivide(temp17, temp18) {
  const a = temp17;
  const b = temp18;
  let temp19;
  try {
    temp19 = a / b;
  } catch (__error) {
    temp19 = 0;
  }
  return temp19;
}
let temp20;
try {
  throw "error";
} catch (__error) {
  const msg = __error;
  temp20 = "caught: " + msg;
}
export const tryCatchWithThrow = temp20;
let temp21;
try {
  throw "critical";
} catch (__error) {
  const err = __error;
  if (err == "critical") {
    temp21 = "critical error";
  } else {
    temp21 = "other error";
  }
}
export const withGuard = temp21;
//# sourceMappingURL=./index.js.map
