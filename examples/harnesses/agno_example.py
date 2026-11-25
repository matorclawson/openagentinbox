from agno.agent import Agent
from python_tools import READ_TOOLS

if __name__ == "__main__":
    Agent(tools=READ_TOOLS).print_response("Use mailbox_identity and report my seat name.")
