"""Execute uniform features and all prompts on every pinned grammar over MCP.

The shared nonempty synthetic source intentionally exercises syntax recovery;
it is not asserted to be valid source in every language. Engine tests also use
curated valid fixtures for thirteen programming languages.
"""
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
    names = sorted(json.loads((BASE/"internal/catalog/parsers.json").read_text())["languages"])
    assert len(names) == 371
    report, calls = [], 0
    with tempfile.TemporaryDirectory(prefix="mcp-all-languages-") as temporary:
        root = Path(temporary)/"project"
        root.mkdir()
        source = "sample = 1\nsample = 2\n"
        (root/"fixture.txt").write_text(source, newline="\n")
        (root/"copy.txt").write_text(source, newline="\n")
        params = server(["--allow-root",str(root),"--state-dir",str(Path(temporary)/"state")])
        async with stdio_client(params) as (read,write):
            async with ClientSession(read,write) as session:
                initialized = await session.initialize()
                assert initialized.serverInfo.version == "1.0.0"
                async def call(tool,args):
                    nonlocal calls
                    result = await session.call_tool(tool,args)
                    calls += 1
                    assert not result.isError, (tool,args,result)
                    data = json.loads(result.content[0].text)
                    assert data == result.structuredContent
                    return data
                await call("register_project",{"project":"matrix","root":str(root)})
                for language in names:
                    common = {"project":"matrix","path":"fixture.txt","language":language}
                    ast = await call("get_ast",common)
                    assert ast["items"]
                    node = ast["items"][0]["node_id"]
                    language_info = await call("get_language_info",{"language":language})
                    assert language_info["language"] == language
                    assert (await call("get_language_info",{"language":language,"section":"node_types","limit":1}))["items"]
                    await call("get_language_info",{"language":language,"section":"fields","limit":1})
                    await call("get_node_at_position",{**common,"start_byte":ast["items"][0]["span"]["start_byte"]})
                    assert (await call("navigate_ast",{**common,"node_id":node}))["items"][0]["node_id"] == node
                    await call("get_parse_diagnostics",common)
                    metrics = await call("get_ast_metrics",common)
                    assert metrics["nodes"] > 0
                    query = "(_) @node\n(ERROR) @error"
                    assert (await call("query_ast",{**common,"query":query}))["items"]
                    multi = await call("query_project",{"project":"matrix","language":language,"path_glob":"fixture.txt","query":query})
                    assert multi["items"] and all(i["status"] == "capture" for i in multi["items"])
                    assert (await call("search_text",{"project":"matrix","pattern":"SAMPLE","ignore_case":True,"whole_word":True,"path_glob":"fixture.txt","context_lines":1}))["items"]
                    snapshot = (await call("create_ast_snapshot",common))["snapshot"]
                    assert not (await call("compare_ast_snapshots",{"project":"matrix","snapshot":snapshot,"other_snapshot":snapshot}))["items"]
                    matches = await call("find_similar_subtrees",{**common,"node_id":node,"other_path":"copy.txt","min_score":1})
                    assert any(i["exact_shape"] for i in matches["items"])
                    for name in ["explain_file","explain_subtree","explain_query","explore_project"]:
                        args = {"project":"matrix"}
                        if name != "explore_project": args.update(path="fixture.txt",language=language)
                        if name == "explain_subtree": args["node_id"] = node
                        if name == "explain_query": args["query"] = query
                        prompt = await session.get_prompt(name,args)
                        assert len(prompt.messages) == 1 and "Evidence JSON:" in prompt.messages[0].content.text
                    await call("clear_cache",{"project":"matrix","path":"fixture.txt"})
                    report.append({"language":language,"status":"passed","nodes":metrics["nodes"],"syntax_errors":metrics["errors"],"missing_nodes":metrics["missing"]})
                await call("unregister_project",{"project":"matrix"})
    print(json.dumps({"status":"passed","version":"1.0.0","transport":"stdio","client":"official Python MCP SDK","grammars_exercised":len(report),"tool_calls":calls,"prompt_calls":4*len(report),"source":"Nonempty shared recovery probe; not universal valid syntax","languages":report},indent=2))
if __name__ == "__main__": asyncio.run(check())
