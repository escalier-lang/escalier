import type * as monaco from 'monaco-editor-core';
import type * as lsp from 'vscode-languageserver-protocol';

/**
 * Returns a function that calls fn once on the next turn of the event loop, however
 * many times it is called before then.
 */
export function coalesced(fn: () => void): () => void {
    let timer: ReturnType<typeof setTimeout> | undefined;
    return () => {
        if (timer !== undefined) {
            return;
        }
        timer = setTimeout(() => {
            timer = undefined;
            fn();
        }, 0);
    };
}

/**
 * The font style each modifier the server reports gives a token. A `mutable` name is
 * underlined, as rust-analyzer shows one, and a `reassignable` name is bold.
 */
const modifierFontStyles = new Map<string, string>([
    ['mutable', 'underline'],
    ['reassignable', 'bold'],
]);

/**
 * Returns the theme rules that style each token type in legend by the modifiers it
 * carries, one rule per combination of the modifiers modifierFontStyles names. Monaco
 * looks a semantic token up in the theme as its type followed by its modifiers in legend
 * order, joined by dots. So `variable.mutable.reassignable` matches a `var mut` binding
 * and gets both styles, and each rule keeps the colour the theme gives the type. A rule
 * matches only a token that carries no modifier outside modifierFontStyles, since such a
 * modifier would join its key.
 */
export function modifierTokenRules(
    legend: lsp.SemanticTokensLegend | undefined,
): monaco.editor.ITokenThemeRule[] {
    if (!legend) {
        return [];
    }
    const styled = legend.tokenModifiers.filter((modifier) =>
        modifierFontStyles.has(modifier),
    );
    const rules: monaco.editor.ITokenThemeRule[] = [];
    // Each non-empty subset of styled, kept in legend order, is one combination.
    for (let mask = 1; mask < 1 << styled.length; mask++) {
        const modifiers = styled.filter((_, i) => mask & (1 << i));
        const fontStyle = modifiers
            .map((modifier) => modifierFontStyles.get(modifier))
            .join(' ');
        for (const tokenType of legend.tokenTypes) {
            rules.push({
                token: [tokenType, ...modifiers].join('.'),
                fontStyle,
            });
        }
    }
    return rules;
}
