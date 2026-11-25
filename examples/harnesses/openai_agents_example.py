from agents import Agent, Runner, function_tool
from python_tools import READ_TOOLS

if __name__ == "__main__":
    agent = Agent(name="Mail assistant", tools=[function_tool(tool) for tool in READ_TOOLS])
    print(Runner.run_sync(agent, "Use mailbox_identity and report my seat name.").final_output)
