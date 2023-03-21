FROM ubuntu:latest

COPY . /app

WORKDIR /app

RUN apt-get update && apt-get install -y golang-go ca-certificates gcc curl

RUN go install

RUN go build -o elrond-adapter

CMD ["./elrond-adapter"]

EXPOSE 5000
