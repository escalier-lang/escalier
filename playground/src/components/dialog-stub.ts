/**
 * Replaces HTMLDialogElement's showModal and close, which jsdom does not implement, with
 * methods that set and clear the element's `open` attribute. A test file that renders a
 * modal dialog under jsdom calls it before its tests run.
 */
export function stubModalDialogs() {
    HTMLDialogElement.prototype.showModal = function () {
        this.open = true;
    };
    HTMLDialogElement.prototype.close = function () {
        this.open = false;
    };
}
