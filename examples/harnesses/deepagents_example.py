from deepagents import create_deep_agent
from python_tools import READ_TOOLS

if __name__ == "__main__":
    agent = create_deep_agent(tools=READ_TOOLS)
    result = agent.invoke({"messages": [{"role": "user", "content": "Use mailbox_identity and report my seat name."}]})
    print(result["messages"][-1].content)
