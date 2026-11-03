## Description
Implement the "View in full width" and horizontal column resizing functionalities for the Resource Dashboard.

- **Full width mode:** Clicking the "View in full width" button hides the queue list column and lets the jobs table expand to 100% width. When active, the toggle changes to "Exit full width" and the resize divider is hidden.
- **Resize divider:** A draggable vertical line sits between the two panels. Dragging adjusts the columns widths dynamically (between 20% and 80%). Double-clicking it resets the split ratio to 50/50.

## Assumptions
- Default split ratio when not full width is 50/50.
- Drag bounds limit ratio between 20% and 80% to prevent either panel from completely collapsing out of view.
- The standard Angular Material styling for buttons is sufficient for the toggle; custom `border-radius: 9999px` overrides standard squared corners.

TAG=agy
CONV=3c6eb5b1-7773-4304-b217-5535f5de61bb
