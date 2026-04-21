/**
 * Styles for the authentication error overlay.
 * These styles are injected dynamically when an auth error occurs.
 */
export const AUTH_ERROR_STYLES = `
body.auth-error-active {
  font-family: Roboto, sans-serif;
  color: #000;
  background-color: #fefefe;
}

.auth-error-overlay .container {
  max-width: 600px;
  width: 90%;
  margin-top: 200px;
  margin-left: auto;
  margin-right: auto;
  text-align: center;
}

.auth-error-overlay .container h1 {
  font-size: 24px;
  font-weight: normal;
  margin-bottom: 45px;
}

.auth-error-overlay .container h3 {
  font-size: 18px;
  font-weight: normal;
  color: #3367d6;
  margin-bottom: 10px;
}

.auth-error-overlay .container li {
  font-size: 14px;
  font-weight: normal;
  line-height: 20px;
}

.auth-error-overlay .container ul {
  text-align: left;
}
`;