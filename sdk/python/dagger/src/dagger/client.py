from typing import NewType

from gql.transport import requests
from gql import Client as gqlClient

SecretID = NewType('SecretID', str)
FSID = NewType('FSID', str)


class Client(gqlClient):

    def __init__(self, host: str = "localhost", port: str = "8080"):
        transport = requests.RequestsHTTPTransport(
            url='http://{}:{}/query'.format(host, port),
            timeout=30,
            retries=10,
        )
        super().__init__(transport=transport,
                         fetch_schema_from_transport=True)