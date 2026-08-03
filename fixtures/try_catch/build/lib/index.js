let temp1;
try {
  temp1 = "success";
} catch (__error) {
  if (true) {
    temp1 = "error";
  }
}
export const basicTryCatch = temp1;
let temp2;
try {
  const x = 5;
  temp2 = x + 10;
} catch (__error) {
  if (true) {
    const y = 0;
    temp2 = y;
  }
}
export const blockBody = temp2;
let temp3;
try {
  throw "x";
} catch (__error) {
  if (true) {
    const e = __error;
    temp3 = e;
  } else if (__error == "x") {
    temp3 = "unreachable";
  }
}
export const catchAllFirst = temp3;
export function guardedIdent(temp4) {
  const n = temp4;
  let temp5;
  try {
    throw "x";
  } catch (__error) {
    if (true) {
      const e = __error;
      if (e == "x" && n > 0) {
        temp5 = 0;
      } else {
        throw __error;
      }
    }
  }
  return temp5;
}
export function guardedWildcard(temp6) {
  const n = temp6;
  let temp7;
  try {
    throw "x";
  } catch (__error) {
    if (true) {
      if (n > 0) {
        temp7 = 0;
      } else {
        throw __error;
      }
    }
  }
  return temp7;
}
let temp8;
try {
  temp8 = 42;
} catch (__error) {
  if (true) {
    temp8 = "error";
  }
}
export const mixedReturn = temp8;
let temp9;
try {
  throw Error("fail");
} catch (__error) {
  if (true) {
    const Error = __error;
    temp9 = "caught error";
  } else {
    temp9 = "unknown";
  }
}
export const multipleCases = temp9;
let temp10;
try {
  let temp11;
  try {
    temp11 = 5;
  } catch (__error) {
    if (true) {
      temp11 = 10;
    }
  }
  temp10 = temp11;
} catch (__error) {
  if (true) {
    temp10 = 0;
  }
}
export const nestedTryCatch = temp10;
let temp12;
try {
  throw {message: "fail"};
} catch (__error) {
  if (__error != null && "message" in __error) {
    const {message: msg} = __error;
    temp12 = msg;
  } else {
    temp12 = "unknown";
  }
}
export const objectPattern = temp12;
let temp13;
try {
  throw "fail";
} catch (__error) {
  if (true) {
    const msg = __error;
    temp13 = msg;
  } else {
    temp13 = "unknown";
  }
}
export const patternBinding = temp13;
export function safeDivide(temp14, temp15) {
  const a = temp14;
  const b = temp15;
  let temp16;
  try {
    temp16 = a / b;
  } catch (__error) {
    if (true) {
      temp16 = 0;
    }
  }
  return temp16;
}
let temp17;
try {
  throw "error";
} catch (__error) {
  if (true) {
    const msg = __error;
    temp17 = "caught: " + msg;
  }
}
export const tryCatchWithThrow = temp17;
let temp18;
try {
  throw "critical";
} catch (__error) {
  if (true) {
    const err = __error;
    if (err == "critical") {
      temp18 = "critical error";
    } else if (true) {
      temp18 = "other error";
    }
  }
}
export const withGuard = temp18;
//# sourceMappingURL=./index.js.map
