/**
 * @fileoverview This file is used to solve CORS issue when calling
 * the experiment state server from the UI.
 * Loading this script in the browser triggers the IAP authentication flow
 * for the backend domain, which sets the necessary authentication cookie
 * for subsequent cross-origin requests made by the application.
 */