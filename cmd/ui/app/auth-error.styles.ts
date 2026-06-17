// Copyright 2025 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
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
