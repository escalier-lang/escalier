export declare function conditionalFetch(useCache: boolean, url: string): Promise<"cached data" | Response>;
export declare function fetchData(url: string): Promise<string>;
export declare function fetchMultiple(url1: string, url2: string): Promise<string>;
export declare function fetchWithAwait(url: string): Promise<Response>;
export declare function fetchWithError(shouldError: boolean): Promise<"success">;
