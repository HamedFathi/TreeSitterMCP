"""Official Python MCP SDK interoperability against the real stdio server."""
import asyncio
import json
import os
from pathlib import Path
import tempfile
from mcp import ClientSession, StdioServerParameters
from mcp.client.stdio import stdio_client
BASE = Path(__file__).resolve().parents[1]

def server(args):
    """Return stdio parameters that start the built server binary directly."""
    exe = BASE/"bin"/("TreeSitterMCP.exe" if os.name == "nt" else "TreeSitterMCP")
    return StdioServerParameters(command=str(exe), args=args, env=dict(os.environ))

async def check():
    calls, tested_prompts = [], []
    with tempfile.TemporaryDirectory(prefix="mcp-test-") as temporary:
        root, state = Path(temporary)/"project", Path(temporary)/"state"
        (root/"src").mkdir(parents=True)
        (root/"empty").mkdir()
        source = "import os\ndef add(a, b):\n    return a+b\n"
        (root/"main.py").write_text(source, newline="\n")
        (root/"src/helper.go").write_text("package helper\n", newline="\n")
        (root/".env").write_text("SECRET=hidden\n", newline="\n")
        params = server(["--allow-root",str(root),"--state-dir",str(state)])
        async with stdio_client(params) as (read, write):
            async with ClientSession(read,write) as session:
                initialized = await session.initialize()
                assert initialized.serverInfo.version == "1.0.0"
                tools = await session.list_tools()
                assert len(tools.tools) == 25
                async def call(name,args,error=False):
                    result = await session.call_tool(name,args)
                    calls.append(name)
                    assert bool(result.isError) == error, (name,result)
                    if error: return None
                    content = json.loads(result.content[0].text)
                    assert result.structuredContent == content
                    return content
                await call("register_project",{"project":"demo","root":str(root)})
                assert len((await call("list_projects",{}))["items"]) == 1
                assert (await call("project_overview",{"project":"demo"}))["file_count"] == 2
                assert len((await call("list_files",{"project":"demo"}))["items"]) == 2
                common = {"project":"demo","path":"main.py"}
                assert "def add" in (await call("read_file",common))["content"]
                ast = await call("get_ast",common)
                node = ast["items"][0]["node_id"]
                assert ast["items"][0]["type"] == "module"
                assert (await call("get_file_metadata",common))["bytes"] == len(source)
                assert (await call("get_language_info",{"language":"python"}))["field_count"] > 0
                assert (await call("get_language_info",{"language":"python","section":"node_types","limit":1}))["next_cursor"]
                position = await call("get_node_at_position",{**common,"line":2,"column":4})
                assert position["type"] == "identifier"
                assert (await call("navigate_ast",{**common,"node_id":position["node_id"],"relation":"parent"}))["items"][0]["type"] == "function_definition"
                assert not (await call("get_parse_diagnostics",common))["items"]
                metrics = await call("get_ast_metrics",common)
                assert metrics["nodes"] > metrics["named_nodes"] and metrics["errors"] == 0
                assert (await call("query_ast",{**common,"query":"(identifier) @id"}))["items"]
                multi = await call("query_project",{"project":"demo","path_glob":"*.py","query":"(identifier) @id"})
                assert all(i["path"] == "main.py" and i["status"] == "capture" for i in multi["items"])
                hits = await call("search_text",{"project":"demo","pattern":"ADD","ignore_case":True,"whole_word":True,"path_glob":"*.py","context_lines":1})
                assert len(hits["items"]) == 1 and len(hits["items"][0]["context"]) == 3
                tree = await call("get_project_tree",{"project":"demo","limit":1})
                assert tree["truncated"] and tree["next_cursor"]
                await call("get_project_tree",{"project":"demo","limit":1,"cursor":tree["next_cursor"]})
                folders = await call("get_project_tree",{"project":"demo","folders_only":True})
                assert any(i.get("empty") and i["path"] == "empty" for i in folders["entries"])
                assert (await call("get_directory_info",{"project":"demo","path":"src"}))["languages"]["go"] == 1
                assert (await call("find_paths",{"project":"demo","pattern":"**/*.go"}))["entries"][0]["path"] == "src/helper.go"
                before = (await call("create_ast_snapshot",common))["snapshot"]
                (root/"copy.py").write_text(source, newline="\n")
                matches = await call("find_similar_subtrees",{**common,"node_id":node,"other_path":"copy.py","min_score":1})
                assert any(i["exact_shape"] and i["score"] == 1 for i in matches["items"])
                prompts = await session.list_prompts()
                assert len(prompts.prompts) == 4
                for prompt in prompts.prompts:
                    args = {"project":"demo"}
                    if prompt.name != "explore_project": args["path"] = "main.py"
                    if prompt.name == "explain_subtree": args["node_id"] = node
                    if prompt.name == "explain_query": args["query"] = "(_) @node"
                    result = await session.get_prompt(prompt.name,args)
                    assert len(result.messages) == 1 and "untrusted" in result.messages[0].content.text
                    tested_prompts.append(prompt.name)
                (root/"main.py").write_text(source.replace("add","sum"), newline="\n")
                after = (await call("create_ast_snapshot",common))["snapshot"]
                assert before != after
                assert (await call("compare_ast_snapshots",{"project":"demo","snapshot":before,"other_snapshot":after}))["items"]
                for tool,args in [
                    ("navigate_ast",{**common,"node_id":node}),
                    ("read_file",{"project":"demo","path":".env"}),
                    ("get_project_tree",{"project":"demo","path":"../outside"}),
                    ("get_ast",{**common,"limit":0}),
                    ("get_node_at_position",{**common,"line":-1}),
                    ("query_ast",{**common,"query":"(no_such_kind) @n"}),
                    ("search_text",{"project":"demo","pattern":"x","context_lines":6})
                ]: await call(tool,args,error=True)
                names,cursor = [],None
                while True:
                    args = {"limit":100}
                    if cursor: args["cursor"] = cursor
                    page = await call("list_languages",args)
                    names += page["items"]
                    cursor = page.get("next_cursor")
                    if not cursor: break
                assert len(names) == len(set(names)) == 371 and "apl" not in names
                assert (await call("cache_stats",{}))["hits"] > 0
        async with stdio_client(params) as (read,write):
            async with ClientSession(read,write) as session:
                await session.initialize()
                async def call2(name,args,error=False):
                    r = await session.call_tool(name,args)
                    calls.append(name)
                    assert bool(r.isError) == error, r
                    return None if error else json.loads(r.content[0].text)
                assert (await call2("list_projects",{}))["items"][0]["name"] == "demo"
                diffargs = {"project":"demo","snapshot":before,"other_snapshot":after}
                assert (await call2("compare_ast_snapshots",diffargs))["items"]
                await call2("clear_cache",common)
                await call2("compare_ast_snapshots",diffargs,error=True)
                await call2("unregister_project",{"project":"demo"})
        assert {t.name for t in tools.tools} == set(calls)
        print(json.dumps({"status":"passed","server_version":initialized.serverInfo.version,"client":"official Python MCP SDK","transport":"stdio","protocol":initialized.protocolVersion,"tools_discovered":len(tools.tools),"tools_exercised":len(set(calls)),"prompts_exercised":sorted(tested_prompts),"calls":len(calls),"canonical_languages":len(names),"registration_and_snapshots_survived_restart":True,"scoped_cache_clear":True,"structured_content_matches_text":True},indent=2))
if __name__ == "__main__": asyncio.run(check())
