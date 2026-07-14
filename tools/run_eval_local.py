#!/usr/bin/env python3
import json
import os
import subprocess
import sys
import urllib.request
import urllib.error
import yaml

# Configuration
REGION = "us-central1"
MODEL = "gemini-flash-latest"  # Use flash-latest as it is verified working
JUDGE_MODEL = "gemini-flash-latest"

def get_token():
    try:
        cmd = ["gcloud", "auth", "application-default", "print-access-token"]
        result = subprocess.run(cmd, capture_output=True, text=True, check=True)
        return result.stdout.strip()
    except subprocess.CalledProcessError as e:
        print(f"Error getting ADC token: {e.stderr}")
        print("Please run: gcloud auth application-default login")
        sys.exit(1)

def get_project():
    cmd = ["gcloud", "config", "get-value", "project"]
    result = subprocess.run(cmd, capture_output=True, text=True, check=True)
    return result.stdout.strip()

def call_gemini(token, project, contents, system_instruction=None, tools=None, response_schema=None):
    url = f"https://generativelanguage.googleapis.com/v1beta/models/{MODEL}:generateContent"
    headers = {
        "Authorization": f"Bearer {token}",
        "Content-Type": "application/json; charset=utf-8",
        "X-Goog-User-Project": project
    }
    
    payload = {
        "contents": contents
    }
    
    if system_instruction:
        payload["systemInstruction"] = {
            "parts": [{"text": system_instruction}]
        }
        
    if tools:
        payload["tools"] = tools
        
    generation_config = {}
    if response_schema:
        generation_config["responseMimeType"] = "application/json"
        generation_config["responseSchema"] = response_schema
        
    if generation_config:
        payload["generationConfig"] = generation_config

    req = urllib.request.Request(url, data=json.dumps(payload).encode("utf-8"), headers=headers, method="POST")
    
    try:
        with urllib.request.urlopen(req) as response:
            res_body = response.read().decode("utf-8")
            return json.loads(res_body)
    except urllib.error.HTTPError as e:
        print(f"API Call failed: {e.code} {e.reason}")
        try:
            print(e.read().decode("utf-8"))
        except:
            pass
        raise e

def call_judge(token, project, trajectory, assertions):
    url = f"https://generativelanguage.googleapis.com/v1beta/models/{JUDGE_MODEL}:generateContent"
    headers = {
        "Authorization": f"Bearer {token}",
        "Content-Type": "application/json; charset=utf-8",
        "X-Goog-User-Project": project
    }
    
    prompt = f"""You are an independent evaluator.
Analyze the following agent execution trajectory (history of interactions, tool calls, and tool outputs).
Verify if the following assertions are met:
{json.dumps(assertions, indent=2)}

Trajectory:
{trajectory}

Respond with a JSON object matching this schema:
{{
  "type": "OBJECT",
  "properties": {{
    "passed": {{
      "type": "BOOLEAN",
      "description": "true if all assertions are met, false otherwise"
    }},
    "explanation": {{
      "type": "STRING",
      "description": "detailed explanation of why it passed or failed, referencing specific steps in the trajectory"
    }}
  }},
  "required": ["passed", "explanation"]
}}
"""
    
    payload = {
        "contents": [{"parts": [{"text": prompt}]}],
        "generationConfig": {
            "responseMimeType": "application/json",
            "responseSchema": {
                "type": "OBJECT",
                "properties": {
                    "passed": {"type": "BOOLEAN"},
                    "explanation": {"type": "STRING"}
                },
                "required": ["passed", "explanation"]
            }
        }
    }
    
    req = urllib.request.Request(url, data=json.dumps(payload).encode("utf-8"), headers=headers, method="POST")
    
    try:
        with urllib.request.urlopen(req) as response:
            res_body = response.read().decode("utf-8")
            res_json = json.loads(res_body)
            text = res_json["candidates"][0]["content"]["parts"][0]["text"]
            return json.loads(text)
    except Exception as e:
        print(f"Judge call failed: {e}")
        return {"passed": False, "explanation": f"Judge failed to execute: {e}"}

def stage_files(files):
    staged = []
    for f in files:
        path = f["path"]
        content = f["content"]
        os.makedirs(os.path.dirname(path), exist_ok=True)
        with open(path, "w") as fh:
            fh.write(content)
        staged.append(path)
        print(f"Staged file: {path}")
    return staged

def cleanup_files(staged_paths):
    for path in staged_paths:
        if os.path.exists(path):
            os.remove(path)
            print(f"Removed staged file: {path}")
            # Try to remove empty parent dirs
            parent = os.path.dirname(path)
            while parent and parent != ".":
                try:
                    os.rmdir(parent)
                    print(f"Removed empty dir: {parent}")
                    parent = os.path.dirname(parent)
                except OSError:
                    break # Not empty

def find_mock(command, mocks):
    for m in mocks:
        mock_cmd = m["command"]
        if command.strip() == mock_cmd.strip():
            return m
    return None

def run_case(token, project, case, skill_md):
    print(f"\n=== Running Case: {case['name']} ===")
    print(f"Description: {case.get('description', '')}")
    
    staged_paths = []
    if "workspace_setup" in case and "files" in case["workspace_setup"]:
        staged_paths = stage_files(case["workspace_setup"]["files"])
        
    try:
        # Define tools
        tools = [{
            "functionDeclarations": [
                {
                    "name": "run_command",
                    "description": "Execute a terminal command. Use this for running cleanup scripts or other CLI tools.",
                    "parameters": {
                        "type": "OBJECT",
                        "properties": {
                            "command": {
                                "type": "STRING",
                                "description": "The command line string to execute."
                            }
                        },
                        "required": ["command"]
                    }
                },
                {
                    "name": "ask_user",
                    "description": "Ask the user for confirmation or input. Use this when the skill guide requires user confirmation.",
                    "parameters": {
                        "type": "OBJECT",
                        "properties": {
                            "question": {
                                "type": "STRING",
                                "description": "The question to ask the user."
                            }
                        },
                        "required": ["question"]
                    }
                }
            ]
        }]
        
        system_instruction = f"""You are Jetski, an expert AI agent.
Your task is to achieve the user's goal by following the provided Skill Guide.
Always run dry-run first if recommended.
Always ask for confirmation before execution if recommended.

Skill Guide:
{skill_md}
"""
        
        contents = [
            {
                "role": "user",
                "parts": [{"text": case["prompt"]}]
            }
        ]
        
        trajectory = []
        trajectory.append(f"User: {case['prompt']}")
        
        max_turns = 10
        turn = 0
        while turn < max_turns:
            print(f"Turn {turn+1}...")
            response = call_gemini(token, project, contents, system_instruction, tools)
            
            candidate = response["candidates"][0]
            content = candidate.get("content", {})
            parts = content.get("parts", [])
            
            # Add model response to history
            contents.append(content)
            
            thought = ""
            function_calls = []
            for part in parts:
                if "text" in part:
                    thought += part["text"]
                if "functionCall" in part:
                    function_calls.append(part["functionCall"])
                    
            if thought:
                print(f"Agent Thought: {thought}")
                trajectory.append(f"Agent: {thought}")
                
            if not function_calls:
                print("Agent finished (no tool calls).")
                trajectory.append("Agent finished.")
                break
                
            # Handle function calls
            response_parts = []
            for call in function_calls:
                name = call["name"]
                args = call["args"]
                print(f"Agent calls tool: {name}({args})")
                trajectory.append(f"Call Tool: {name}({args})")
                
                if name == "run_command":
                    cmd = args["command"]
                    mock = find_mock(cmd, case.get("tool_mocks", []))
                    if mock:
                        stdout = mock.get("stdout", "")
                        stderr = mock.get("stderr", "")
                        exit_code = mock.get("exit_code", 0)
                        print(f"Mocked output for '{cmd}' (exit {exit_code})")
                        response_content = {"stdout": stdout, "stderr": stderr, "exit_code": exit_code}
                    else:
                        print(f"WARNING: Unmocked command: {cmd}")
                        trajectory.append(f"System Error: Unmocked command {cmd}")
                        response_content = {"stdout": "", "stderr": "Error: Command not mocked in eval config.", "exit_code": 1}
                        # We can choose to fail here or let the agent handle it.
                        # For safety, we return error to agent.
                elif name == "ask_user":
                    question = args["question"]
                    # Simulate user saying "yes" to confirmation
                    print(f"Simulating user response to: {question} -> 'yes'")
                    response_content = {"response": "yes"}
                else:
                    response_content = {"error": f"Unknown tool {name}"}
                    
                response_parts.append({
                    "functionResponse": {
                        "name": name,
                        "response": response_content
                    }
                })
                trajectory.append(f"Tool Response: {response_content}")
                
            # Add responses to history
            contents.append({
                "role": "function",
                "parts": response_parts
            })
            
            turn += 1
            
        if turn >= max_turns:
            print("Reached max turns.")
            trajectory.append("Error: Reached max turns.")
            
        # Run Evaluation
        print("Evaluating trajectory...")
        trajectory_str = "\n".join(trajectory)
        result = call_judge(token, project, trajectory_str, case["outcome_assertions"])
        
        print(f"Result: {'PASSED' if result['passed'] else 'FAILED'}")
        print(f"Explanation: {result['explanation']}")
        return result["passed"]
        
    finally:
        cleanup_files(staged_paths)

def main():
    if len(sys.argv) < 2:
        print("Usage: run_eval_local.py <path_to_EVAL.yaml>")
        sys.exit(1)
        
    eval_file = sys.argv[1]
    if not os.path.exists(eval_file):
        print(f"File not found: {eval_file}")
        sys.exit(1)
        
    # Read skill file (assuming co-located SKILL.md)
    eval_dir = os.path.dirname(eval_file)
    skill_file = os.path.join(eval_dir, "SKILL.md")
    if not os.path.exists(skill_file):
        # Try parent dir
        skill_file = os.path.join(eval_dir, "..", "SKILL.md")
        if not os.path.exists(skill_file):
            print(f"Skill file not found near {eval_file}")
            sys.exit(1)
            
    with open(skill_file, "r") as f:
        skill_md = f.read()
        
    with open(eval_file, "r") as f:
        eval_config = yaml.safe_load(f)
        
    token = get_token()
    project = get_project()
    
    passed_count = 0
    total_count = 0
    
    for case in eval_config.get("cases", []):
        total_count += 1
        if run_case(token, project, case, skill_md):
            passed_count += 1
            
    print(f"\n=== Summary ===")
    print(f"Passed: {passed_count}/{total_count} ({passed_count/total_count*100:.1f}%)")
    
    if passed_count != total_count:
        sys.exit(1)

if __name__ == "__main__":
    main()
