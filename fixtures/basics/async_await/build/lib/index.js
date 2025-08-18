export async function fetchData(temp1) {
  const url = temp1;
  return "data from " + url;
}
export async function fetchWithAwait(temp2) {
  const url = temp2;
  const data = await fetch(url);
  return data;
}
export async function fetchWithError(temp3) {
  const shouldError = temp3;
  let temp4;
  if (shouldError) {
    let temp5;
    throw "Something went wrong";
    temp4 = temp5;
  }
  temp4;
  return "success";
}
export async function fetchMultiple(temp6, temp7) {
  const url1 = temp6;
  const url2 = temp7;
  const data1 = await fetch(url1);
  const data2 = await fetch(url2);
  return data1 + " and " + data2;
}
export async function conditionalFetch(temp8, temp9) {
  const useCache = temp8;
  const url = temp9;
  let temp10;
  if (useCache) {
    return "cached data";
    temp10 = undefined;
  } else {
    const data = await fetch(url);
    return data;
    temp10 = undefined;
  }
  temp10;
}
//# sourceMappingURL=./index.js.map
